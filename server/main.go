package main

import (
	"context"
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

	"github.com/nathanxiangang-web/cilihub/server/internal/commands"
	"github.com/nathanxiangang-web/cilihub/server/internal/filmcatalog"
	"github.com/nathanxiangang-web/cilihub/server/internal/media"
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

	line := newLineStore(filepath.Join(dataDir, "line.json"), envOr("CILIHUB_UPSTREAM", defaultUpstream), logger)

	dist, err := web.Dist()
	if err != nil {
		logger.Fatalf("静态资源加载失败: %v（请先运行 npm run build:web 生成 server/web/dist）", err)
	}

	mediaSvc := media.New()

	mux := http.NewServeMux()
	deps := commands.Deps{
		Base:    line.Base,
		SetBase: line.SetBase,
		Media:   mediaSvc,
		Log:     logger,
	}
	if credentialFile := os.Getenv("OPENLIST_CREDENTIALS_FILE"); credentialFile != "" {
		catalog, err := filmcatalog.New(os.Getenv("OPENLIST_ORIGIN"), os.Getenv("OPENLIST_FOLDER"),
			credentialFile, os.Getenv("OPENLIST_CDN_HOST"))
		if err != nil {
			logger.Fatalf("OpenList 视频目录配置失败: %v", err)
		}
		deps.Base = func() string { return catalog.Origin }
		deps.SetBase = func(string) (string, error) { return "", errors.New("此实例固定使用 OpenList 视频目录") }
		deps.CatalogEdge = catalog.Edge
		mux.Handle(filmcatalog.MediaPrefix, catalog)
		go catalog.Run(context.Background(), logger.Printf)
	}
	commands.Register(mux, deps)
	mux.Handle(media.Prefix+"/", mediaSvc.Handler())
	mux.Handle("/", spaHandler(dist))

	srv := &http.Server{
		Addr:              envOr("CILIHUB_LISTEN", ":8080"),
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}
	logger.Printf("cilihub 监听 %s，上游线路 %s", srv.Addr, deps.Base())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("HTTP 服务退出: %v", err)
	}
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
