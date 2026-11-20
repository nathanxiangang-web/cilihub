package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/adminapi"
	"github.com/nathanxiangang-web/cilihub/server/internal/adminauth"
	"github.com/nathanxiangang-web/cilihub/server/internal/commands"
	"github.com/nathanxiangang-web/cilihub/server/internal/filmcatalog"
	"github.com/nathanxiangang-web/cilihub/server/internal/media"
	"github.com/nathanxiangang-web/cilihub/server/internal/secret"
	"github.com/nathanxiangang-web/cilihub/server/internal/shares"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
	"github.com/nathanxiangang-web/cilihub/server/web"
)

const defaultUpstream = "https://huangguoai.com"

func main() {
	dataDir := envOr("CILIHUB_DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("创建数据目录 %s 失败: %v", dataDir, err)
	}

	logger, closer := newLogger(dataDir)
	if closer != nil {
		defer closer.Close()
	}

	if err := secret.Configure(dataDir); err != nil {
		logger.Fatalf("初始化凭据加密失败: %v", err)
	}

	dsn := strings.TrimSpace(os.Getenv("CILIHUB_DATABASE_URL"))
	if dsn == "" {
		logger.Fatalf("未配置 CILIHUB_DATABASE_URL，例如 postgres://cilihub:cilihub@db:5432/cilihub?sslmode=disable")
	}
	db, err := store.Open(dsn)
	if err != nil {
		logger.Fatalf("连接 PostgreSQL 失败: %v", err)
	}
	defer db.Close()
	ensureAdmin(db, logger)

	line := newLineStore(filepath.Join(dataDir, "line.json"), envOr("CILIHUB_UPSTREAM", defaultUpstream), logger)

	dist, err := web.Dist()
	if err != nil {
		logger.Fatalf("静态资源加载失败: %v（请先运行 npm run build:web 生成 server/web/dist）", err)
	}

	mediaSvc := media.New()

	mux := http.NewServeMux()
	adminDeps := adminapi.Deps{Store: db, Log: logger}
	deps := commands.Deps{
		Base:    line.Base,
		SetBase: line.SetBase,
		Media:   mediaSvc,
		Log:     logger,
		Store:   db,
	}
	catalog := filmcatalog.NewUnconfigured(db)
	now := time.Now().UTC().Format(time.RFC3339)
	settings, err := db.OpenListSettings()
	switch {
	case err == nil:
		password, decodeErr := secret.Open(settings.PasswordEnc)
		if decodeErr != nil {
			logger.Printf("无法解密已保存的 OpenList 密码（请到后台重新保存连接）: %v", decodeErr)
		} else if applyErr := catalog.Apply(filmcatalog.ListConfig{
			Origin:   settings.Origin,
			Folder:   settings.Folder,
			CDNHost:  settings.CDNHost,
			Username: settings.Username,
			Password: password,
		}, now); applyErr != nil {
			logger.Printf("应用库中的 OpenList 配置失败: %v", applyErr)
		}
	case errors.Is(err, store.ErrNotFound):
		if credentialFile, ok := envOpenListReady(); ok {
			configured, newErr := filmcatalog.New(os.Getenv("OPENLIST_ORIGIN"), os.Getenv("OPENLIST_FOLDER"),
				credentialFile, os.Getenv("OPENLIST_CDN_HOST"), db)
			if newErr != nil {
				logger.Fatalf("OpenList 视频目录配置失败: %v", newErr)
			}
			catalog = configured
			if importErr := importOpenListSettings(db, credentialFile, now); importErr != nil {
				logger.Printf("导入 env 中的 OpenList 连接失败: %v", importErr)
			}
		}
	default:
		logger.Fatalf("读取 OpenList 配置失败: %v", err)
	}

	adminDeps.Catalog = catalog
	deps.Base = func() string { return catalog.Origin }
	deps.SetBase = func(string) (string, error) { return "", errors.New("此实例固定使用 OpenList 视频目录") }
	deps.CatalogEdge = catalog.Edge
	mux.Handle(filmcatalog.MediaPrefix, catalog)
	go catalog.Run(context.Background(), logger.Printf)

	shareSvc := shares.New(db, catalog, logger)
	deps.Shares = shareSvc

	adminapi.Register(mux, adminDeps)
	commands.Register(mux, deps)
	mux.Handle(media.Prefix+"/", mediaSvc.Handler())
	// 比 /media/ 更具体，ServeMux 会优先匹配分享播放路由。
	mux.Handle("/media/share/", shareSvc.Handler())
	mux.Handle("/", spaHandler(dist))

	srv := &http.Server{
		Addr:              envOr("CILIHUB_LISTEN", ":8080"),
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}
	logger.Printf("cilihub 监听 %s，OpenList 已配置：%v", srv.Addr, catalog.Configured())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("HTTP 服务退出: %v", err)
	}
}

// envOpenListReady 判断 env 是否提供了可用的一次性导入配置：origin 与凭据文件都在且文件可读。
func envOpenListReady() (string, bool) {
	file := strings.TrimSpace(os.Getenv("OPENLIST_CREDENTIALS_FILE"))
	if file == "" || strings.TrimSpace(os.Getenv("OPENLIST_ORIGIN")) == "" {
		return "", false
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return "", false
	}
	return file, true
}

// importOpenListSettings 把 env + 凭据文件中的连接加密写入库，之后以库中配置为准。
func importOpenListSettings(db *store.Store, credentialFile, now string) error {
	raw, err := os.ReadFile(credentialFile)
	if err != nil {
		return err
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		return errors.New("OpenList 凭据文件格式无效")
	}
	if creds.Username == "" || creds.Password == "" {
		return errors.New("OpenList 凭据文件需要 username 和 password")
	}
	enc, err := secret.Seal(creds.Password)
	if err != nil {
		return err
	}
	return db.SaveOpenListSettings(store.OpenListSettings{
		Origin:      strings.TrimSpace(os.Getenv("OPENLIST_ORIGIN")),
		Folder:      strings.TrimSpace(os.Getenv("OPENLIST_FOLDER")),
		CDNHost:     strings.TrimSpace(os.Getenv("OPENLIST_CDN_HOST")),
		Username:    creds.Username,
		PasswordEnc: enc,
	}, now)
}

// ensureAdmin 仅在库中还没有管理员时创建首个账号；密码为空则随机生成并打印一次。
func ensureAdmin(db *store.Store, logger *log.Logger) {
	count, err := db.AdminCount()
	if err != nil {
		logger.Fatalf("读取管理员数量失败: %v", err)
	}
	if count > 0 {
		return
	}
	username := envOr("CILIHUB_ADMIN_USER", "admin")
	password := strings.TrimSpace(os.Getenv("CILIHUB_ADMIN_PASSWORD"))
	generated := false
	if password == "" {
		password, err = randomPassword(16)
		if err != nil {
			logger.Fatalf("生成初始管理员密码失败: %v", err)
		}
		generated = true
	}
	hash, err := adminauth.HashPassword(password)
	if err != nil {
		logger.Fatalf("初始化管理员密码失败: %v", err)
	}
	if _, err := db.CreateAdmin(username, hash, time.Now().UTC().Format(time.RFC3339)); err != nil {
		logger.Fatalf("创建初始管理员 %s 失败: %v", username, err)
	}
	if generated {
		logger.Printf("已创建管理员 %s，初始密码：%s（请登录 /admin 后立即修改）", username, password)
		return
	}
	logger.Printf("已创建管理员 %s（密码取自 CILIHUB_ADMIN_PASSWORD）", username)
}

func randomPassword(n int) (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

// newLogger 同时写 stdout 与 <data-dir>/debug.log；日志文件不可写时只写 stdout。
func newLogger(dir string) (*log.Logger, io.Closer) {
	f, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return log.New(os.Stdout, "", log.LstdFlags), nil
	}
	return log.New(io.MultiWriter(os.Stdout, f), "", log.LstdFlags), f
}

// lineStore 持有当前上游 origin 并持久化到 <data-dir>/line.json（{"base":"https://..."}）。
type lineStore struct {
	mu   sync.RWMutex
	base string
	path string
	log  *log.Logger
}

func newLineStore(path, fallback string, logger *log.Logger) *lineStore {
	s := &lineStore{base: normalizeLine(fallback), path: path, log: logger}
	if s.base == "" {
		s.base = defaultUpstream
	}
	if text, err := os.ReadFile(path); err == nil {
		var saved struct {
			Base string `json:"base"`
		}
		if json.Unmarshal(text, &saved) == nil {
			if next := normalizeLine(saved.Base); next != "" {
				s.base = next
			}
		}
	}
	return s
}

func (s *lineStore) Base() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.base
}

func (s *lineStore) SetBase(raw string) (string, error) {
	next := normalizeLine(raw)
	if next == "" {
		return "", errors.New("线路地址无效（需要纯 HTTPS 站点地址，禁止内网）")
	}
	s.mu.Lock()
	s.base = next
	s.mu.Unlock()

	out, err := json.Marshal(struct {
		Base string `json:"base"`
	}{Base: next})
	if err == nil {
		if err = os.WriteFile(s.path, out, 0o644); err != nil {
			s.log.Printf("线路落盘失败 %s: %v", s.path, err)
		}
	}
	return next, nil
}

// normalizeLine 只接受纯 HTTPS origin（无路径、认证、查询串或片段），返回规整后的地址。
func normalizeLine(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	if u.Path != "" && u.Path != "/" {
		return ""
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// spaHandler 命中同名文件则返回文件，否则回落 index.html；/assets/* 未命中直接 404。
func spaHandler(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "." {
			http.ServeFileFS(w, r, dist, "index.html")
			return
		}
		if info, err := fs.Stat(dist, name); err == nil && !info.IsDir() {
			http.ServeFileFS(w, r, dist, name)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, dist, "index.html")
	})
}