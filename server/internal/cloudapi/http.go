package cloudapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
	"github.com/nathanxiangang-web/cilihub/server/internal/userauth"
)

const privatePath = "/api/cloud-downloads"
const collectorPath = "/api/internal/collector/downloads"

type Config struct {
	CookieFile, PrivateRoot, CollectorRoot, CollectorTokenFile, CDNHost string
	MagcrawOrigin, MagcrawKeyFile                                       string
}

func Register(mux *http.ServeMux, db *store.Store, cfg Config, logger *log.Logger) (*Service, error) {
	s, err := newService(db, cfg, logger)
	if err != nil {
		return nil, err
	}
	s.magnets, err = newMagSearch(cfg.MagcrawOrigin, cfg.MagcrawKeyFile)
	if err != nil {
		return nil, err
	}
	s.register(mux, func(r *http.Request) (int64, error) {
		token := userauth.TokenFrom(r)
		if token == "" {
			return 0, store.ErrNotFound
		}
		user, err := db.UserBySession(userauth.HashToken(token), time.Now().UTC().Format(time.RFC3339))
		return user.ID, err
	})
	return s, nil
}

type identityFunc func(*http.Request) (int64, error)

func (s *Service) register(mux *http.ServeMux, identify identityFunc) {
	protect := http.NewCrossOriginProtection()
	private := func(f func(http.ResponseWriter, *http.Request, int64)) http.Handler {
		return protect.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			id, err := identify(r)
			if errors.Is(err, store.ErrNotFound) || (err == nil && id <= 0) {
				fail(w, 401, "请先登录")
				return
			}
			if err != nil {
				fail(w, 503, "账号服务暂时不可用")
				return
			}
			f(w, r, id)
		}))
	}
	mux.Handle("GET "+privatePath, private(s.list))
	mux.Handle("POST "+privatePath, private(s.queue))
	mux.Handle("POST "+privatePath+"/search", private(s.startSearch))
	mux.Handle("GET "+privatePath+"/search/{key}", private(s.searchStatus))
	mux.Handle("GET "+privatePath+"/{id}/files/{index}", private(s.play))
	collector := func(f func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if len(s.collectorKey) == 0 {
				fail(w, 404, "采集通道未启用")
				return
			}
			value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(value), s.collectorKey) != 1 {
				fail(w, 401, "采集认证失败")
				return
			}
			f(w, r, 0)
		}
	}
	mux.HandleFunc("POST "+collectorPath, collector(s.queue))
	mux.HandleFunc("GET "+collectorPath+"/{id}", collector(s.detail))
}

type taskView struct {
	ID       int64      `json:"id"`
	State    string     `json:"state"`
	Message  string     `json:"message"`
	Title    string     `json:"title"`
	Progress *float64   `json:"progress"`
	Files    []fileView `json:"files"`
}
type fileView struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url,omitempty"`
}

func view(task store.CloudTask) taskView {
	out := taskView{task.ID, task.State, task.Message, task.Title, task.Progress, []fileView{}}
	for i, f := range task.Files {
		file := fileView{Name: f.Name, Size: f.Size}
		if task.Owner > 0 {
			file.URL = fmt.Sprintf("%s/%d/files/%d", privatePath, task.ID, i)
		}
		out.Files = append(out.Files, file)
	}
	return out
}

func (s *Service) queue(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.source == nil || (owner > 0 && !s.private) {
		fail(w, 503, "云下载暂未配置")
		return
	}
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		fail(w, 415, "请求须使用 JSON")
		return
	}
	var request struct {
		Magnet   string `json:"magnet"`
		Title    string `json:"title"`
		Identity string `json:"identity"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		fail(w, 400, "提交参数无效")
		return
	}
	magnet, hash, err := normalizeMagnet(request.Magnet)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if owner > 0 {
		request.Identity = ""
		if strings.TrimSpace(request.Title) == "" {
			parsed, _ := url.Parse(magnet)
			request.Title = parsed.Query().Get("dn")
		}
		if len(request.Title) > 1024 || strings.ContainsRune(request.Title, 0) {
			request.Title = ""
		}
	}
	request.Title = strings.TrimSpace(request.Title)
	if len(request.Title) > 1024 || len(request.Identity) > 512 || strings.ContainsRune(request.Identity, 0) || strings.ContainsRune(request.Title, 0) {
		fail(w, 400, "资源名称过长或无效")
		return
	}
	if request.Title == "" {
		request.Title = "磁力任务 " + hash[:8]
	}
	task, created, err := s.db.QueueCloud(owner, hash, magnet, request.Identity, request.Title)
	if err != nil {
		fail(w, 503, "暂时无法登记任务，请稍后刷新")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	write(w, status, map[string]any{"created": created, "task": view(task)})
}

func (s *Service) list(w http.ResponseWriter, r *http.Request, owner int64) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	tasks, err := s.db.CloudTasks(owner, max(0, before))
	if err != nil {
		fail(w, 503, "暂时无法读取私人任务")
		return
	}
	views := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, view(t))
	}
	write(w, 200, map[string]any{"tasks": views, "enabled": s.private && s.source != nil, "search_enabled": s.magnets != nil})
}

func (s *Service) lookup(w http.ResponseWriter, r *http.Request, owner int64) (store.CloudTask, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		fail(w, 404, "找不到任务")
		return store.CloudTask{}, false
	}
	t, err := s.db.CloudTask(owner, id)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "找不到任务")
		return t, false
	}
	if err != nil {
		fail(w, 503, "任务查询暂时不可用")
		return t, false
	}
	return t, true
}

func (s *Service) detail(w http.ResponseWriter, r *http.Request, owner int64) {
	if t, ok := s.lookup(w, r, owner); ok {
		write(w, 200, view(t))
	}
}

func (s *Service) play(w http.ResponseWriter, r *http.Request, owner int64) {
	t, ok := s.lookup(w, r, owner)
	if !ok {
		return
	}
	i, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || i < 0 || i >= len(t.Files) || t.State != "ready" {
		fail(w, 404, "找不到视频")
		return
	}
	if s.source == nil {
		fail(w, 503, "云下载暂未配置")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	target, err := s.source.link(ctx, t.Folder, t.Files[i], r.UserAgent())
	if err != nil {
		fail(w, 502, "暂时无法获取 115 播放地址")
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Length", "0")
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusFound)
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	write(w, status, map[string]string{"error": message})
}
