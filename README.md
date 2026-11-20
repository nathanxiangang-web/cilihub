# 磁力短剧（cilihub）

从 OpenList 递归读取影视目录，将视频文件平铺到上下滑播放流。播放器上方显示画面，完整文件名在画面下方。可用滚轮、方向键或触摸滑动换片，也可以按照文件名搜索。

## 模块与依赖

| 目录 | 职责 |
| --- | --- |
| `src/components/Feed.tsx`、`VideoSurface.tsx` | 播放流、滑动切换、文件名以及浏览器播放器 |
| `server/internal/filmcatalog/openlist.go` | OpenList 登录、会话更新、目录分页和实时取链 |
| `server/internal/filmcatalog/catalog.go` | 递归遍历、视频筛选和定期发布目录快照 |
| `server/internal/filmcatalog/adapter.go` | 目录适配成 Web RPC，并为有效文件标识生成 302 |
| `server/web/embed.go` | 将已构建的 Web 资源嵌入 Go 二进制 |

目录按账号的相对路径读取。缓存包含文件标识和元数据。每次访问播放地址时，以浏览器的 User-Agent 向 OpenList 查询当前临时 URL，对允许的 HTTPS CDN 返回 302 空响应。浏览器直接请求 CDN，播放支持取决于原生解码器。新增视频格式应同时更新目录筛选并验证浏览器播放。更新上游接口时在 OpenList 客户端适配，播放路由始终只接受目录内的文件标识。

## Docker 部署

1. 复制 `.env.example` 为 `.env`，填写 OpenList HTTPS 地址、相对目录和允许的 CDN 主机。
2. 将 OpenList 账号写入本地 JSON 配置的 `username` 和 `password` 字段，设置 UID/GID `10001` 可读的 `0400` 权限。用 `OPENLIST_CREDENTIALS_PATH` 指定路径。
3. 执行 `bash build.sh`，再执行 `docker compose up -d --wait`。

默认监听服务器上的 `127.0.0.1:18792`。通过 SSH 转发到本机后访问该端口。用 `docker compose down` 停止服务。

## 从源码构建

使用 Node.js 22、pnpm 10 和 Go 1.23 或更高版本。运行 `pnpm install --frozen-lockfile` 和 `pnpm run build` 将 Web 产物写入 Go 嵌入目录。进入 `server` 后运行 `go test ./...` 和 `go build -o cilihub .`。直接运行二进制时，设置 `OPENLIST_ORIGIN`、`OPENLIST_FOLDER`、`OPENLIST_CDN_HOST`、`OPENLIST_CREDENTIALS_FILE` 和 `CILIHUB_LISTEN`。

## 来源与许可

Web 播放界面来自 [MovieHub](https://github.com/nathanxiangang-web/moviehub/tree/830aa941714eeabc57be3ac24b98922bbca1a328)，OpenList 适配基于 [302test](https://github.com/nathanxiangang-web/302test/tree/aa96a1f5ece73d80d1e7e54d3d3c9f9913e3a929)。原项目 [HGhub](https://github.com/Minireel/HGhub) 的版权与发行要求见 [LICENSE](LICENSE)。项目仅限非商业使用。
