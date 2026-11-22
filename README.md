# 磁力短剧（cilihub）

从 OpenList 递归读取影视目录，将视频文件平铺到上下滑播放流。播放器上方显示画面，完整文件名在画面下方。可用滚轮、方向键或触摸滑动换片，也可以按照文件名搜索。

## 模块与依赖

| 目录 | 职责 |
| --- | --- |
| `src/components/Feed.tsx`、`VideoSurface.tsx` | 播放流、滑动切换、文件名以及浏览器播放器 |
| `src/admin/*` | `/admin` 管理控制台（登录、概览、影视库、分类与首页、同步、诊断、系统与审计） |
| `server/internal/store` | PostgreSQL 数据层（作品、集、分类、同步记录、管理员、审计） |
| `server/internal/adminauth` | 管理员密码哈希、会话 cookie 与身份解析 |
| `server/internal/adminapi` | `/admin/api/*` 后台接口 |
| `server/internal/filmcatalog/openlist.go` | OpenList 登录、会话更新、目录分页和实时取链 |
| `server/internal/filmcatalog/catalog.go` | 递归扫描、与库合并和 5 分钟周期同步 |
| `server/internal/filmcatalog/adapter.go` | 按库中可见性提供列表/详情接口，并为有效文件标识生成 302 |
| `server/web/embed.go` | 将已构建的 Web 资源嵌入 Go 二进制 |

目录按账号的相对路径只读读取。作品、集、分类与上下架状态全部存于 PostgreSQL（连接串 `CILIHUB_DATABASE_URL`，Go 侧驱动 `pgx/v5/stdlib`），是本项目唯一的运营数据来源。每次访问播放地址时，以浏览器的 User-Agent 向 OpenList 查询当前临时 URL，对允许的 HTTPS CDN 返回 302 空响应，视频字节不经过服务器。新增视频格式应同时更新目录筛选并验证浏览器播放。更新上游接口时在 OpenList 客户端适配，播放路由始终只接受库内的文件标识。

## 数据库

- 独立容器 `cilihub-db`：`postgres:18-alpine`，库名/用户/密码均为 `cilihub`，数据保存在命名卷 `cilihub-db`。
- `web` 通过 `depends_on: db(service_healthy)` 等数据库就绪后再启动；未设置 `CILIHUB_DATABASE_URL` 时进程会直接报错退出并提示连接串格式。
- 连接池上限 10、空闲 5、单连接最长 30 分钟；所有写入在事务内完成。
- 备份：`docker compose exec -T db pg_dump -U cilihub -d cilihub > cilihub.sql`；恢复：`docker compose exec -T db psql -U cilihub -d cilihub < cilihub.sql`。

## 管理后台

- **入口**：<http://127.0.0.1:18792/admin>（SSH 转发后用本机浏览器打开）。前台 SPA 与后台共用同一份构建产物，`/admin` 前缀挂载独立控制台。
- **首次登录**：库中还没有管理员时，启动会创建 `CILIHUB_ADMIN_USER`（默认 `admin`）。若未设置 `CILIHUB_ADMIN_PASSWORD`，会随机生成 16 位密码并打印到日志（`docker compose logs web`，或数据目录下的 `debug.log`）。首次登录后请在“系统与审计”页修改密码。
- **六项功能**：
  1. 概览：作品总数、已上架数、缺失文件数与最近一次同步结果。
  2. 影视库：关键词/分类/状态筛选，批量上下架，修改标题与分类、置顶，调整集序、把文件移动到其他作品，抽屉内直接预览播放。
  3. 分类与首页：新建/改名/隐藏分类、调整分类顺序，编排首页置顶与首页顺序。
  4. OpenList 同步：查看最近同步状态、历史记录与新增/恢复/缺失事件，可手动触发一次同步。
  5. 播放诊断：针对单个文件分别检查库内快照、OpenList 实时取链与浏览器播放地址，区分链接失败、站点 302 与浏览器解码失败。
  6. 系统与审计：修改密码（其他会话会被注销），查看全部写操作审计记录。
- **数据与同步规则**：同步每 5 分钟扫描一次 OpenList（失败后 30 秒重试）。扫描只登记文件的新增、恢复与缺失，不覆盖运营维护的标题、分类、可见性、置顶和排序；前台列表、详情与播放全部按库中的可见性工作。`/data` 卷仅保存 `line.json` 与 `debug.log`，运营数据在 PostgreSQL 的 `cilihub-db` 卷中，备份用 `pg_dump`（见上节）。

## Docker 部署

1. 复制 `.env.example` 为 `.env`，填写 OpenList HTTPS 地址、相对目录和允许的 CDN 主机，并按需设置管理员账号与密码。`CILIHUB_DATABASE_URL` 保持默认即连到 compose 内置的 `db` 服务。
2. 将 OpenList 账号写入本地 JSON 配置的 `username` 和 `password` 字段，设置 UID/GID `10001` 可读的 `0400` 权限。用 `OPENLIST_CREDENTIALS_PATH` 指定路径。
3. 执行 `bash build.sh`，再执行 `docker compose up -d --wait`。首次启动会自动创建 `db` 容器、初始化表结构与默认分类，并按需创建管理员。

默认监听服务器上的 `127.0.0.1:18792`。通过 SSH 转发到本机后访问该端口。用 `docker compose down` 停止服务（加 `-v` 会同时删除数据库卷，谨慎使用）。

## 从源码构建

使用 Node.js 22、pnpm 10 和 Go 1.23 或更高版本。运行 `pnpm install --frozen-lockfile` 和 `pnpm run build` 将 Web 产物写入 Go 嵌入目录。进入 `server` 后运行 `go mod tidy`（首次拉取 `pgx/v5`）、`go test ./...` 和 `go build -o cilihub .`。直接运行二进制时，设置 `OPENLIST_ORIGIN`、`OPENLIST_FOLDER`、`OPENLIST_CDN_HOST`、`OPENLIST_CREDENTIALS_FILE`、`CILIHUB_DATABASE_URL`、`CILIHUB_LISTEN` 以及可选的 `CILIHUB_ADMIN_USER`、`CILIHUB_ADMIN_PASSWORD`。

## 来源与许可

Web 播放界面来自 [MovieHub](https://github.com/nathanxiangang-web/moviehub/tree/830aa941714eeabc57be3ac24b98922bbca1a328)，OpenList 适配基于 [302test](https://github.com/nathanxiangang-web/302test/tree/aa96a1f5ece73d80d1e7e54d3d3c9f9913e3a929)。原项目 [HGhub](https://github.com/Minireel/HGhub) 的版权与发行要求见 [LICENSE](LICENSE)。项目仅限非商业使用。
## 私人云下载与运营采集

登录用户可以从「我的」下方打开「云下载」，提交 BTIH 磁力链接并查看 115 排队、下载和索引状态。结果只列出 MP4，播放时按会话检查当前账号与任务的归属，再向配置的 115 CDN 返回 302。相同链接在同一账号下复用已有任务。若 115 账号已经存在相同哈希的任务，且目标目录属于其他空间，当前任务会显示冲突。

服务端的 `cloudapi` 负责会话校验、队列推进和 115driver 适配，`store` 保存任务与文件索引，`filmcatalog` 通过保留目录前缀排除私人下载。运营采集程序独立部署，通过受令牌保护的任务接口提交并查询，完工后由 OpenList 定期同步运营目录的视频。

配置 `CILIHUB_MAGCRAW_ORIGIN` 和 `CILIHUB_MAGCRAW_KEY_FILE` 后，用户可在「搜索」页用同一搜索框输入关键词并点击「搜索磁力」，结果直接在搜索页出现。CiliHub 服务端通过 magcraw 的任务接口查询结果，浏览器按当前登录账号回查自己的搜索。选中的磁力加入该账号的私人下载任务。两边使用同一个只读令牌文件，magcraw 通过 `CI_API_KEY_FILE` 加载。

用 `docker compose -f compose.yaml -f compose.cloud.yaml up -d` 加载云下载配置。设置需要启用的 115 目录 ID，`CILIHUB_115_PRIVATE_ROOT` 对应私人下载，`CILIHUB_115_COLLECTOR_ROOT` 对应运营下载。两个空间使用不同目录。私人目录放在 OpenList 公开扫描根目录之外，运营目录映射到公开扫描根或其子目录。采集任务在运营目录下各用独立子目录，前台平铺其中的 MP4。把 115 凭据放在只读挂载目录的 `cookie` 文件中。启用运营时还需把至少 32 字符的共享令牌放在该目录的 `collector-token` 文件中。两个文件需允许容器用户 10001 读取。省略云下载覆盖文件可按原配置启动。
