// Package filmcatalog adapts a read-only OpenList folder to the site's video APIs
// and merges each completed scan into the SQLite library.
package filmcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

const MediaPrefix = "/media/library/"

// ListConfig 是后台保存/测试连接时使用的完整连接参数；Password 仅入参，不回传。
type ListConfig struct {
	Origin   string
	Folder   string
	CDNHost  string
	Username string
	Password string
}

// DirEntry 是目录浏览返回的子目录（不含文件）。
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

const maxBrowseDirs = 500

type Adapter struct {
	mu         sync.RWMutex
	catalog    catalog
	cdnHost    string
	username   string
	configured bool
	Origin     string
}

func New(origin, folder, credentialFile, cdnHost string, st *store.Store) (*Adapter, error) {
	if err := validateFolder(folder); err != nil {
		return nil, err
	}
	if err := validateCDNHost(cdnHost); err != nil {
		return nil, err
	}
	if st == nil {
		return nil, errors.New("影片目录需要数据库支持")
	}
	client, err := newOpenList(origin, credentialFile)
	if err != nil {
		return nil, err
	}
	return &Adapter{
		catalog:    catalog{source: client, root: folder, store: st},
		cdnHost:    cdnHost,
		username:   client.creds.Username,
		configured: true,
		Origin:     client.origin,
	}, nil
}

// NewUnconfigured 在没有任何源配置时启动；Configured 为 false，Run 会等待 Apply。
func NewUnconfigured(st *store.Store) *Adapter {
	return &Adapter{catalog: catalog{store: st}}
}

// Apply 校验后替换源与目录并异步触发一次同步；校验失败保持现有配置不变。
func (a *Adapter) Apply(cfg ListConfig, now string) error {
	if err := validateFolder(cfg.Folder); err != nil {
		return err
	}
	if err := validateCDNHost(cfg.CDNHost); err != nil {
		return err
	}
	client, err := newOpenListWithCreds(cfg.Origin, cfg.Username, cfg.Password)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.catalog.mu.Lock()
	a.catalog.source = client
	a.catalog.root = cfg.Folder
	a.catalog.mu.Unlock()
	a.cdnHost = cfg.CDNHost
	a.username = cfg.Username
	a.Origin = client.origin
	a.configured = true
	a.mu.Unlock()

	go func() {
		if _, err := a.SyncNow(); err != nil {
			log.Printf("OpenList 目录刷新失败: %v", err)
		}
	}()
	return nil
}

// BrowseDirs 读取指定目录的子目录；dir 为空时读取当前配置的映射目录。
// 返回归一化后的绝对路径，仅包含目录项，按名称升序，最多 500 条。
func (a *Adapter) BrowseDirs(ctx context.Context, dir string) (string, []DirEntry, error) {
	a.mu.RLock()
	src, root, configured := a.catalog.source, a.catalog.root, a.configured && a.catalog.source != nil
	a.mu.RUnlock()
	if !configured {
		return "", nil, errors.New("尚未配置 OpenList 连接")
	}

	target := strings.TrimSpace(dir)
	if target == "" {
		target = root
	}
	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}
	target = path.Clean(target)

	entries, err := src.list(ctx, target)
	if err != nil {
		return "", nil, browseError(err)
	}
	dirs := make([]DirEntry, 0, len(entries))
	for _, item := range entries {
		if !item.Dir || item.Name == "" || item.Name == "." || item.Name == ".." || strings.ContainsAny(item.Name, "/\\\x00") {
			continue
		}
		dirs = append(dirs, DirEntry{Name: item.Name, Path: path.Join(target, item.Name)})
	}
	slices.SortFunc(dirs, func(x, y DirEntry) int { return strings.Compare(x.Name, y.Name) })
	if len(dirs) > maxBrowseDirs {
		dirs = dirs[:maxBrowseDirs]
	}
	return target, dirs, nil
}

func browseError(err error) error {
	var status openListStatus
	if !errors.As(err, &status) {
		return err
	}
	switch int(status) {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("账号或密码不正确")
	case http.StatusNotFound, http.StatusInternalServerError:
		// OpenList 对不存在的路径返回 500，两种都按“目录不可读”提示。
		return errors.New("目录不存在或无法读取该目录")
	default:
		return status
	}
}

func (a *Adapter) Config() ListConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return ListConfig{Origin: a.Origin, Folder: a.catalog.root, CDNHost: a.cdnHost, Username: a.username}
}

func (a *Adapter) Configured() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.configured && a.catalog.source != nil
}

func (a *Adapter) snapshot() (src source, cdnHost string, ok bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.catalog.source, a.cdnHost, a.configured && a.catalog.source != nil
}

func validateFolder(folder string) error {
	if !strings.HasPrefix(folder, "/") || path.Clean(folder) != folder || folder == "/" || strings.Contains(folder, "\\") {
		return errors.New("OPENLIST_FOLDER 必须是账号下的指定子目录")
	}
	return nil
}

func validateCDNHost(cdnHost string) error {
	if cdnHost == "" || strings.ContainsAny(cdnHost, ":/@?#\\ ") {
		return errors.New("OPENLIST_CDN_HOST 必须是明确的主机名")
	}
	return nil
}

func (a *Adapter) Run(ctx context.Context, logf func(string, ...any)) {
	a.catalog.run(ctx, logf)
}

func (a *Adapter) SyncNow() (int64, error) {
	return a.catalog.syncNow()
}

func (a *Adapter) Edge(_ context.Context, method, resource string) ([]byte, error) {
	if !a.Configured() {
		return nil, errors.New("尚未配置 OpenList 连接")
	}
	u, err := url.ParseRequestURI(resource)
	if err != nil {
		return nil, errors.New("影片请求无效")
	}
	if method == http.MethodGet {
		switch {
		case u.Path == "/api/videos":
			page, size := pageParams(u.Query())
			return feedPage(a.catalog.store.FeedWithSeed("", "", u.Query().Get("seed"), page, size))
		case strings.HasPrefix(u.Path, "/api/videos/category/"):
			slug := strings.TrimPrefix(u.Path, "/api/videos/category/")
			if slug == "" || strings.Contains(slug, "/") {
				return nil, errors.New("分类不存在")
			}
			page, size := pageParams(u.Query())
			return feedPage(a.catalog.store.Feed(slug, "", page, size))
		case u.Path == "/api/search":
			page, size := pageParams(u.Query())
			return feedPage(a.catalog.store.Feed("", strings.TrimSpace(u.Query().Get("q")), page, size))
		case u.Path == "/api/posts":
			return json.Marshal(emptyPage(u.Query()))
		}
		if strings.HasPrefix(u.Path, "/api/ranks/") {
			return json.Marshal(map[string]any{"type": path.Base(u.Path), "items": []any{}})
		}
	}
	if !strings.HasPrefix(u.Path, "/api/videos/") {
		return nil, errors.New("影片库仅支持浏览和播放已上架的影片")
	}
	idText, action, _ := strings.Cut(strings.TrimPrefix(u.Path, "/api/videos/"), "/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("视频不存在")
	}
	detail, err := a.catalog.store.GetWork(id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !detail.Work.Visible) {
		return nil, errors.New("视频不存在")
	}
	if err != nil {
		return nil, err
	}
	// 切换目录后会留下 present=false 的旧作品：前台不应再进入其详情/播放页。
	if detail.Work.EpisodeCount == 0 {
		return nil, errors.New("视频不存在")
	}
	switch {
	case method == http.MethodGet && action == "":
		counts, err := a.catalog.store.WorkCounts(id)
		if err != nil {
			return nil, errors.New("读取互动计数失败：" + err.Error())
		}
		data := workJSON(detail)
		data["like_count"] = counts.Likes
		data["comment_count"] = counts.Comments
		data["collect_count"] = counts.Favorites
		// 前端优先读 counts.*，两处必须一致
		data["counts"] = map[string]int64{
			"like": counts.Likes, "comment": counts.Comments,
			"collect": counts.Favorites, "hot": 0,
		}
		return json.Marshal(data)
	case method == http.MethodGet && action == "comments":
		return json.Marshal(emptyPage(u.Query()))
	case method == http.MethodPost && strings.HasPrefix(action, "episodes/") && strings.HasSuffix(action, "/playback"):
		return a.playbackJSON(detail, action)
	}
	return nil, errors.New("影片库仅支持浏览和播放已上架的影片")
}

func (a *Adapter) playbackJSON(detail store.WorkDetail, action string) ([]byte, error) {
	epText := strings.TrimSuffix(strings.TrimPrefix(action, "episodes/"), "/playback")
	ep, err := strconv.Atoi(epText)
	if err != nil {
		return nil, errors.New("集数无效")
	}
	if !detail.Work.Visible {
		return nil, errors.New("影片已下架")
	}
	for _, file := range detail.Files {
		if file.EpisodeNo == ep && file.Present {
			return json.Marshal(map[string]any{
				"id": detail.Work.ID, "title": detail.Work.Title, "ep_num": ep,
				"duration": 0, "video_url": MediaPrefix + strconv.FormatInt(file.FileID, 10),
			})
		}
	}
	return nil, errors.New("源文件缺失")
}

type Diagnostics struct {
	Catalog  DiagnosticCatalog  `json:"catalog"`
	OpenList DiagnosticOpenList `json:"openlist"`
	Playback DiagnosticPlayback `json:"playback"`
}

type DiagnosticCatalog struct {
	InSnapshot bool `json:"in_snapshot"`
}

type DiagnosticOpenList struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Reason    string `json:"reason"`
}

type DiagnosticPlayback struct {
	URL  string `json:"url"`
	Mode string `json:"mode"`
}

func (a *Adapter) Diagnose(ctx context.Context, fileID int64) (Diagnostics, error) {
	src, cdnHost, configured := a.snapshot()
	if !configured {
		err := errors.New("尚未配置 OpenList 连接")
		return Diagnostics{
			OpenList: DiagnosticOpenList{Reason: err.Error()},
			Playback: DiagnosticPlayback{Mode: "error"},
		}, err
	}
	ref, err := a.catalog.store.VisibleFile(fileID)
	if err != nil {
		return Diagnostics{
			OpenList: DiagnosticOpenList{Reason: "文件不在库中"},
			Playback: DiagnosticPlayback{Mode: "error"},
		}, err
	}
	diag := Diagnostics{
		Catalog:  DiagnosticCatalog{InSnapshot: ref.Present},
		Playback: DiagnosticPlayback{Mode: "error"},
	}
	started := time.Now()
	target, linkErr := src.link(ctx, ref.Path, "")
	diag.OpenList.ElapsedMS = time.Since(started).Milliseconds()
	switch {
	case linkErr != nil:
		diag.OpenList.Status = http.StatusBadGateway
		diag.OpenList.Reason = "OpenList 未返回可直连的影片地址"
	default:
		if _, err := directURL(cdnHost, target); err != nil {
			diag.OpenList.Status = http.StatusBadGateway
			diag.OpenList.Reason = err.Error()
			break
		}
		diag.OpenList.OK = true
		diag.OpenList.Status = http.StatusOK
		diag.Playback = DiagnosticPlayback{
			URL:  MediaPrefix + strconv.FormatInt(fileID, 10),
			Mode: "302",
		}
	}
	if !ref.Visible {
		diag.Playback = DiagnosticPlayback{Mode: "error"}
		diag.OpenList.Reason = "影片已下架"
	}
	if !ref.Present {
		diag.Playback = DiagnosticPlayback{Mode: "error"}
		diag.OpenList.Reason = "源文件缺失"
	}
	return diag, nil
}

func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "仅支持播放请求", http.StatusMethodNotAllowed)
		return
	}
	src, cdnHost, configured := a.snapshot()
	if !configured {
		http.Error(w, "视频不存在", http.StatusNotFound)
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, MediaPrefix), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "视频不存在", http.StatusNotFound)
		return
	}
	ref, err := a.catalog.store.VisibleFile(id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "视频不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "读取影片信息失败", http.StatusInternalServerError)
		return
	}
	if store.IsCloudPrivatePath(ref.Path) {
		http.Error(w, "视频不存在", http.StatusNotFound)
		return
	}
	if !ref.Visible {
		http.Error(w, "影片已下架", http.StatusForbidden)
		return
	}
	if !ref.Present {
		http.Error(w, "源文件缺失", http.StatusNotFound)
		return
	}
	target, err := src.link(r.Context(), ref.Path, r.UserAgent())
	if err == nil {
		var direct *url.URL
		direct, err = directURL(cdnHost, target)
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
	return directURL(a.cdnHost, item)
}

func directURL(cdnHost string, item link) (*url.URL, error) {
	u, err := url.Parse(item.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" ||
		!strings.EqualFold(u.Hostname(), cdnHost) || (u.Port() != "" && u.Port() != "443") ||
		item.Dir == nil || *item.Dir {
		return nil, fmt.Errorf("文件未提供允许的 HTTPS CDN 直链")
	}
	return u, nil
}

func feedPage(page store.FeedPage, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, card(item.ID, item.Title, item.EpisodeCount, item.TotalEpisodes))
	}
	return json.Marshal(map[string]any{
		"items": items,
		"pagination": map[string]int{
			"page": page.Page, "size": page.Size, "pages": page.Pages, "total": page.Total,
		},
	})
}

func card(id int64, title string, episodeCount, total int) map[string]any {
	return map[string]any{
		"id": id, "title": title, "cover": "", "tags": []string{}, "score": 0,
		"episode_count": episodeCount, "total_episodes": total, "is_finished": true, "is_original": 0,
		"hot": 0, "like_count": 0, "comment_count": 0, "collect_count": 0,
		"author": map[string]any{"id": 0, "name": "OpenList"},
	}
}

func workJSON(detail store.WorkDetail) map[string]any {
	data := card(detail.Work.ID, detail.Work.Title, detail.Work.EpisodeCount, detail.Work.EpisodeCount)
	data["description"] = ""
	episodes := make([]map[string]any, 0, len(detail.Files))
	for _, file := range detail.Files {
		if !file.Present {
			continue
		}
		episodes = append(episodes, map[string]any{
			"ep_num": file.EpisodeNo, "title": file.FileName, "duration": 0, "status": 1,
		})
	}
	data["episodes"] = episodes
	data["counts"] = map[string]int{"collect": 0, "comment": 0, "hot": 0, "like": 0}
	return data
}

func emptyPage(query url.Values) any {
	page, size := pageParams(query)
	return map[string]any{
		"items": []map[string]any{},
		"pagination": map[string]int{
			"page": page, "size": size, "pages": 0, "total": 0,
		},
	}
}

func pageParams(query url.Values) (int, int) {
	page, _ := strconv.Atoi(query.Get("page"))
	size, _ := strconv.Atoi(query.Get("size"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 24
	}
	return page, size
}
