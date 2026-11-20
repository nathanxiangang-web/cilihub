// Package filmcatalog adapts a read-only OpenList folder to MovieHub's video RPCs.
package filmcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const MediaPrefix = "/media/library/"

type Adapter struct {
	catalog catalog
	cdnHost string
	Origin  string
}

func New(origin, folder, credentialFile, cdnHost string) (*Adapter, error) {
	if !strings.HasPrefix(folder, "/") || path.Clean(folder) != folder || folder == "/" || strings.Contains(folder, "\\") {
		return nil, errors.New("OPENLIST_FOLDER 必须是账号下的指定子目录")
	}
	if cdnHost == "" || strings.ContainsAny(cdnHost, ":/@?#\\ ") {
		return nil, errors.New("OPENLIST_CDN_HOST 必须是明确的主机名")
	}
	client, err := newOpenList(origin, credentialFile)
	if err != nil {
		return nil, err
	}
	return &Adapter{catalog: catalog{source: client, root: folder}, cdnHost: cdnHost, Origin: client.origin}, nil
}

func (a *Adapter) Run(ctx context.Context, logf func(string, ...any)) {
	a.catalog.run(ctx, logf)
}

func (a *Adapter) Edge(_ context.Context, method, resource string) ([]byte, error) {
	snapshot := a.catalog.current.Load()
	if snapshot == nil {
		return nil, errors.New("OpenList 正在读取视频目录，请稍后重试")
	}
	u, err := url.ParseRequestURI(resource)
	if err != nil {
		return nil, errors.New("影片请求无效")
	}
	if method == http.MethodGet {
		switch u.Path {
		case "/api/videos", "/api/search", "/api/videos/category/openlist-films":
			return json.Marshal(pageOf(snapshot.films, u.Query()))
		case "/api/posts":
			return json.Marshal(pageOf(nil, u.Query()))
		}
		if strings.HasPrefix(u.Path, "/api/ranks/") {
			return json.Marshal(map[string]any{"type": path.Base(u.Path), "items": []any{}})
		}
	}
	if strings.HasPrefix(u.Path, "/api/videos/") {
		idText, action, _ := strings.Cut(strings.TrimPrefix(u.Path, "/api/videos/"), "/")
		id, _ := strconv.ParseInt(idText, 10, 64)
		item, ok := snapshot.byID[id]
		switch {
		case ok && method == http.MethodGet && action == "":
			return json.Marshal(item.detail())
		case ok && method == http.MethodGet && action == "comments":
			return json.Marshal(pageOf(nil, u.Query()))
		case ok && method == http.MethodPost && action == "episodes/1/playback":
			return json.Marshal(map[string]any{
				"id": item.ID, "title": item.Title, "ep_num": 1,
				"video_url": MediaPrefix + strconv.FormatInt(item.ID, 10),
			})
		}
	}
	return nil, errors.New("OpenList 影片库仅支持浏览和播放已列出的影片")
}

func (f film) card() map[string]any {
	return map[string]any{
		"id": f.ID, "title": f.Title, "cover": "", "tags": []string{}, "score": 0,
		"episode_count": 1, "total_episodes": 1, "is_finished": true, "is_original": 0,
		"hot": 0, "like_count": 0, "comment_count": 0, "collect_count": 0,
		"author": map[string]any{"id": 0, "name": "OpenList"},
	}
}

func (f film) detail() map[string]any {
	data := f.card()
	data["description"] = ""
	data["episodes"] = []map[string]any{{"ep_num": 1, "title": f.Title, "duration": 0, "status": 1}}
	data["counts"] = map[string]int{"collect": 0, "comment": 0, "hot": 0, "like": 0}
	return data
}

func pageOf(films []film, query url.Values) any {
	keyword := strings.ToLower(strings.TrimSpace(query.Get("q")))
	if keyword != "" {
		matches := make([]film, 0)
		for _, item := range films {
			if strings.Contains(strings.ToLower(item.Title), keyword) {
				matches = append(matches, item)
			}
		}
		films = matches
	}
	page, _ := strconv.Atoi(query.Get("page"))
	size, _ := strconv.Atoi(query.Get("size"))
	if size < 1 || size > 100 {
		size = 24
	}
	pages := (len(films) + size - 1) / size
	page = min(max(page, 1), max(pages, 1))
	start := (page - 1) * size
	items := make([]map[string]any, 0, min(size, len(films)-start))
	for _, item := range films[start:min(start+size, len(films))] {
		items = append(items, item.card())
	}
	return map[string]any{
		"items":      items,
		"pagination": map[string]int{"page": page, "size": size, "pages": pages, "total": len(films)},
	}
}

func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "仅支持播放请求", http.StatusMethodNotAllowed)
		return
	}
	current := a.catalog.current.Load()
	if current == nil {
		http.Error(w, "视频目录正在载入", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, MediaPrefix), 10, 64)
	item, ok := current.byID[id]
	if err != nil || !ok {
		http.Error(w, "视频不存在", http.StatusNotFound)
		return
	}
	target, err := a.catalog.source.link(r.Context(), item.path, r.UserAgent())
	if err == nil {
		var direct *url.URL
		direct, err = a.directURL(target)
		if err == nil {
			w.Header().Set("Content-Length", "0")
			w.Header().Set("Location", direct.String())
			w.WriteHeader(http.StatusFound)
			return
		}
	}
	http.Error(w, "OpenList 未返回可直连的影片地址", http.StatusBadGateway)
}

func (a *Adapter) directURL(item link) (*url.URL, error) {
	u, err := url.Parse(item.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" ||
		!strings.EqualFold(u.Hostname(), a.cdnHost) || (u.Port() != "" && u.Port() != "443") ||
		item.Dir == nil || *item.Dir {
		return nil, fmt.Errorf("文件未提供允许的 HTTPS CDN 直链")
	}
	return u, nil
}
