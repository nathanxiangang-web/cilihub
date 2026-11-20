// Package adminapi 提供 /admin/api/* 后台接口：登录、影视库维护、分类首页编排、同步、诊断与审计。
package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/adminauth"
	"github.com/nathanxiangang-web/cilihub/server/internal/filmcatalog"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

const (
	prefix          = "/admin/api"
	maxBodyBytes    = 1 << 20
	maxAuditBytes   = 2048
	maxBulkIDs      = 200
	defaultPageSize = 20
	auditPageSize   = 50
	maxPageSize     = 200
	maxListPages    = 1000
)

// Catalog 是后台需要的目录同步与运行时配置能力。
type Catalog interface {
	SyncNow() (int64, error)
	Diagnose(ctx context.Context, fileID int64) (filmcatalog.Diagnostics, error)
	Apply(cfg filmcatalog.ListConfig, now string) error
	Configured() bool
	BrowseDirs(ctx context.Context, path string) (string, []filmcatalog.DirEntry, error)
}

// Deps 是后台接口依赖的进程级资源。
type Deps struct {
	Store   *store.Store
	Catalog Catalog
	Log     *log.Logger
}

// Register 把 /admin/api/* 挂到 mux。
func Register(mux *http.ServeMux, deps Deps) {
	if deps.Store == nil || deps.Log == nil {
		panic("adminapi: Deps.Store 与 Deps.Log 不可为空")
	}
	mux.Handle(prefix+"/", &router{deps: deps})
}

type router struct{ deps Deps }

type userView struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type workView struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	CategoryID   int64  `json:"category_id"`
	CategoryName string `json:"category_name"`
	Visible      bool   `json:"visible"`
	Pinned       bool   `json:"pinned"`
	SortIndex    int    `json:"sort_index"`
	EpisodeCount int    `json:"episode_count"`
	MissingCount int    `json:"missing_count"`
	UpdatedAt    string `json:"updated_at"`
}

type fileView struct {
	FileID       int64  `json:"file_id"`
	FileName     string `json:"file_name"`
	EpisodeNo    int    `json:"episode_no"`
	Present      bool   `json:"present"`
	SizeBytes    int64  `json:"size_bytes"`
	MissingSince string `json:"missing_since"`
}

type categoryView struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	SortIndex int    `json:"sort_index"`
	Visible   bool   `json:"visible"`
	WorkCount int    `json:"work_count"`
}

type runView struct {
	ID         int64  `json:"id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Status     string `json:"status"`
	Added      int    `json:"added"`
	Missing    int    `json:"missing"`
	Failed     int    `json:"failed"`
	Message    string `json:"message"`
}

type eventView struct {
	ID       int64  `json:"id"`
	RunID    int64  `json:"run_id"`
	Kind     string `json:"kind"`
	FilePath string `json:"file_path"`
	Message  string `json:"message"`
	At       string `json:"at"`
}

type auditView struct {
	At         string `json:"at"`
	Admin      string `json:"admin"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Detail     string `json:"detail"`
}

type pagination struct {
	Page  int `json:"page"`
	Size  int `json:"size"`
	Pages int `json:"pages"`
	Total int `json:"total"`
}

func (h *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	seg := pathSegments(r.URL.Path)

	if len(seg) == 1 && seg[0] == "login" {
		if allowMethod(w, r, http.MethodPost) {
			h.login(w, r)
		}
		return
	}
	if len(seg) == 1 && seg[0] == "session" {
		if allowMethod(w, r, http.MethodGet) {
			h.session(w, r)
		}
		return
	}

	admin, err := adminauth.AdminFrom(r, h.deps.Store)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}

	switch {
	case len(seg) == 1 && seg[0] == "logout":
		if allowMethod(w, r, http.MethodPost) {
			h.logout(w, r, admin)
		}
	case len(seg) == 1 && seg[0] == "password":
		if allowMethod(w, r, http.MethodPost) {
			h.changePassword(w, r, admin)
		}
	case len(seg) == 1 && seg[0] == "overview":
		if allowMethod(w, r, http.MethodGet) {
			h.overview(w, r, admin)
		}
	case len(seg) == 1 && seg[0] == "users":
		switch r.Method {
		case http.MethodGet:
			h.listUsers(w, r, admin)
		case http.MethodPost:
			h.createUser(w, r, admin)
		default:
			allowMethod(w, r, http.MethodGet, http.MethodPost)
		}
	case len(seg) == 2 && seg[0] == "users":
		switch r.Method {
		case http.MethodPost:
			h.updateUser(w, r, admin, seg[1])
		case http.MethodDelete:
			h.deleteUser(w, r, admin, seg[1])
		default:
			allowMethod(w, r, http.MethodPost, http.MethodDelete)
		}
	case len(seg) == 1 && seg[0] == "shares":
		if allowMethod(w, r, http.MethodGet) {
			h.listShares(w, r, admin)
		}
	case len(seg) == 3 && seg[0] == "shares" && seg[2] == "revoke":
		if allowMethod(w, r, http.MethodPost) {
			h.revokeShare(w, r, admin, seg[1])
		}
	case len(seg) == 3 && seg[0] == "shares" && seg[2] == "restore":
		if allowMethod(w, r, http.MethodPost) {
			h.restoreShare(w, r, admin, seg[1])
		}
	case len(seg) == 2 && seg[0] == "shares":
		if allowMethod(w, r, http.MethodDelete) {
			h.deleteShare(w, r, admin, seg[1])
		}
	case len(seg) == 1 && seg[0] == "comments":
		if allowMethod(w, r, http.MethodGet) {
			h.listComments(w, r, admin)
		}
	case len(seg) == 2 && seg[0] == "comments":
		switch r.Method {
		case http.MethodPost:
			h.updateComment(w, r, admin, seg[1])
		case http.MethodDelete:
			h.deleteComment(w, r, admin, seg[1])
		default:
			allowMethod(w, r, http.MethodPost, http.MethodDelete)
		}
	case len(seg) == 1 && seg[0] == "works":
		if allowMethod(w, r, http.MethodGet) {
			h.listWorks(w, r, admin)
		}
	case len(seg) == 2 && seg[0] == "works" && seg[1] == "visibility":
		if allowMethod(w, r, http.MethodPost) {
			h.setVisibility(w, r, admin)
		}
	case len(seg) == 2 && seg[0] == "works" && seg[1] == "assign":
		if allowMethod(w, r, http.MethodPost) {
			h.assignFiles(w, r, admin)
		}
	case len(seg) == 3 && seg[0] == "works" && seg[2] == "episodes":
		if allowMethod(w, r, http.MethodPost) {
			h.reorderEpisodes(w, r, admin, seg[1])
		}
	case len(seg) == 2 && seg[0] == "works":
		switch r.Method {
		case http.MethodGet:
			h.getWork(w, r, admin, seg[1])
		case http.MethodPost:
			h.patchWork(w, r, admin, seg[1])
		default:
			allowMethod(w, r, http.MethodGet, http.MethodPost)
		}
	case len(seg) == 1 && seg[0] == "categories":
		switch r.Method {
		case http.MethodGet:
			h.listCategories(w, r, admin)
		case http.MethodPost:
			h.createCategory(w, r, admin)
		default:
			allowMethod(w, r, http.MethodGet, http.MethodPost)
		}
	case len(seg) == 2 && seg[0] == "categories" && seg[1] == "reorder":
		if allowMethod(w, r, http.MethodPost) {
			h.reorderCategories(w, r, admin)
		}
	case len(seg) == 2 && seg[0] == "categories":
		if allowMethod(w, r, http.MethodPost) {
			h.updateCategory(w, r, admin, seg[1])
		}
	case len(seg) == 1 && seg[0] == "home":
		switch r.Method {
		case http.MethodGet:
			h.getHome(w, r, admin)
		case http.MethodPost:
			h.setHome(w, r, admin)
		default:
			allowMethod(w, r, http.MethodGet, http.MethodPost)
		}
	case len(seg) == 1 && seg[0] == "sync":
		if allowMethod(w, r, http.MethodGet) {
			h.syncState(w, r, admin)
		}
	case len(seg) == 2 && seg[0] == "sync" && seg[1] == "run":
		if allowMethod(w, r, http.MethodPost) {
			h.runSync(w, r, admin)
		}
	case len(seg) == 1 && seg[0] == "diagnostics":
		if allowMethod(w, r, http.MethodGet) {
			h.diagnostics(w, r, admin)
		}
	case len(seg) == 1 && seg[0] == "audit":
		if allowMethod(w, r, http.MethodGet) {
			h.auditList(w, r, admin)
		}
	case len(seg) == 2 && seg[0] == "openlist" && seg[1] == "test":
		if allowMethod(w, r, http.MethodPost) {
			h.openlistTest(w, r, admin)
		}
	case len(seg) == 2 && seg[0] == "openlist" && seg[1] == "browse":
		if allowMethod(w, r, http.MethodGet) {
			h.openlistBrowse(w, r, admin)
		}
	case len(seg) == 1 && seg[0] == "openlist":
		switch r.Method {
		case http.MethodGet:
			h.openlistState(w, r, admin)
		case http.MethodPost:
			h.openlistSave(w, r, admin)
		default:
			allowMethod(w, r, http.MethodGet, http.MethodPost)
		}
	default:
		writeError(w, http.StatusNotFound, "接口不存在")
	}
}

func (h *router) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		writeError(w, http.StatusOK, "请输入用户名和密码")
		return
	}

	user, err := h.deps.Store.FindAdmin(username)
	if err != nil || !adminauth.VerifyPassword(user.PasswordHash, req.Password) {
		if err != nil {
			h.deps.Log.Printf("管理员 %q 登录失败: %v", username, err)
		}
		writeError(w, http.StatusOK, "用户名或密码不正确")
		return
	}

	token, tokenHash := adminauth.NewToken()
	now := nowString()
	expiresAt := time.Now().UTC().Add(adminauth.SessionTTL).Format(time.RFC3339)
	if err := h.deps.Store.PutSession(tokenHash, user.ID, expiresAt, now); err != nil {
		h.fail(w, "创建会话失败："+err.Error(), err)
		return
	}
	adminauth.SetCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserView(user)})
}

func (h *router) session(w http.ResponseWriter, r *http.Request) {
	admin, err := adminauth.AdminFrom(r, h.deps.Store)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"user": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserView(admin)})
}

func (h *router) logout(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	if cookie, err := r.Cookie(adminauth.CookieName); err == nil && cookie.Value != "" {
		if err := h.deps.Store.DeleteSession(adminauth.HashToken(cookie.Value)); err != nil {
			h.deps.Log.Printf("删除后台会话失败: %v", err)
		}
	}
	adminauth.ClearCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) changePassword(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if !adminauth.VerifyPassword(admin.PasswordHash, req.OldPassword) {
		writeError(w, http.StatusOK, "原密码不正确")
		return
	}
	if n := utf8.RuneCountInString(req.NewPassword); n < 1 || n > 128 {
		writeError(w, http.StatusOK, "新密码长度需为 1-128 个字符")
		return
	}

	hash, err := adminauth.HashPassword(req.NewPassword)
	if err != nil {
		h.fail(w, "生成密码哈希失败："+err.Error(), err)
		return
	}
	if err := h.deps.Store.UpdateAdminPassword(admin.ID, hash); err != nil {
		h.fail(w, "修改密码失败："+err.Error(), err)
		return
	}
	if cookie, err := r.Cookie(adminauth.CookieName); err == nil && cookie.Value != "" {
		if err := h.deps.Store.DeleteOtherSessions(admin.ID, adminauth.HashToken(cookie.Value)); err != nil {
			h.deps.Log.Printf("清理其他会话失败 admin=%d: %v", admin.ID, err)
		}
	}
	h.audit(admin, "admin.password", "admin", strconv.FormatInt(admin.ID, 10), auditDetail(map[string]any{
		"username": admin.Username,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) audit(admin store.AdminUser, action, targetType, targetID, detail string) {
	if err := h.deps.Store.Audit(admin.ID, action, targetType, targetID, detail, nowString()); err != nil {
		h.deps.Log.Printf("写入审计失败 action=%s target=%s/%s: %v", action, targetType, targetID, err)
	}
}

func (h *router) fail(w http.ResponseWriter, msg string, err error) {
	h.deps.Log.Printf("%s: %v", msg, err)
	writeError(w, http.StatusOK, msg)
}

// allWorks 逐页读取全部作品，供总览与首页编排使用。
func (h *router) allWorks() ([]store.WorkRow, error) {
	out := make([]store.WorkRow, 0, 64)
	for page := 1; page <= maxListPages; page++ {
		res, err := h.deps.Store.ListWorks(store.WorksFilter{Page: page, Size: maxPageSize})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Items...)
		if len(res.Items) == 0 || page >= res.Pages {
			break
		}
	}
	return out, nil
}

func allowMethod(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "请求方法不正确")
	return false
}

func pathSegments(p string) []string {
	p = strings.Trim(strings.TrimPrefix(p, prefix), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"__admin_error": msg})
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func parseID(raw string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func normalizePage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

func normalizeSize(size int) int {
	if size < 1 {
		return defaultPageSize
	}
	if size > maxPageSize {
		return maxPageSize
	}
	return size
}

// auditDetail 把变更摘要编码为 JSON，超过 2 KB 时只保留截断预览。
func auditDetail(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	if len(raw) <= maxAuditBytes {
		return string(raw)
	}
	preview := raw
	if len(preview) > 800 {
		preview = preview[:800]
	}
	out, err := json.Marshal(map[string]any{
		"truncated": true,
		"bytes":     len(raw),
		"preview":   strings.ToValidUTF8(string(preview), ""),
	})
	if err != nil {
		return ""
	}
	return string(out)
}

func toUserView(user store.AdminUser) userView {
	return userView{ID: user.ID, Username: user.Username}
}

func toWorkView(row store.WorkRow) workView {
	return workView{
		ID:           row.ID,
		Title:        row.Title,
		CategoryID:   row.CategoryID,
		CategoryName: row.CategoryName,
		Visible:      row.Visible,
		Pinned:       row.Pinned,
		SortIndex:    row.SortIndex,
		EpisodeCount: row.EpisodeCount,
		MissingCount: row.MissingCount,
		UpdatedAt:    row.UpdatedAt,
	}
}

func toFileView(row store.FileRow) fileView {
	return fileView{
		FileID:       row.FileID,
		FileName:     row.FileName,
		EpisodeNo:    row.EpisodeNo,
		Present:      row.Present,
		SizeBytes:    row.SizeBytes,
		MissingSince: row.MissingSince,
	}
}

func toCategoryView(row store.CategoryRow) categoryView {
	return categoryView{
		ID:        row.ID,
		Slug:      row.Slug,
		Name:      row.Name,
		SortIndex: row.SortIndex,
		Visible:   row.Visible,
		WorkCount: row.WorkCount,
	}
}

func toRunView(run store.SyncRun) runView {
	return runView{
		ID:         run.ID,
		StartedAt:  run.StartedAt,
		FinishedAt: run.FinishedAt,
		Status:     run.Status,
		Added:      run.Added,
		Missing:    run.Missing,
		Failed:     run.Failed,
		Message:    run.Message,
	}
}

func toEventView(ev store.SyncEvent) eventView {
	return eventView{
		ID:       ev.ID,
		RunID:    ev.RunID,
		Kind:     ev.Kind,
		FilePath: ev.FilePath,
		Message:  ev.Message,
		At:       ev.At,
	}
}

func toAuditView(row store.AuditRow) auditView {
	return auditView{
		At:         row.At,
		Admin:      row.AdminName,
		Action:     row.Action,
		TargetType: row.TargetType,
		TargetID:   row.TargetID,
		Detail:     row.Detail,
	}
}

func atOf(run store.SyncRun) string {
	if run.FinishedAt != "" {
		return run.FinishedAt
	}
	return run.StartedAt
}