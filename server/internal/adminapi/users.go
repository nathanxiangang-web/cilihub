package adminapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
	"github.com/nathanxiangang-web/cilihub/server/internal/userauth"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

type siteUserView struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Status      string `json:"status"`
	Note        string `json:"note"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at"`
}

func (h *router) listUsers(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "active" && status != "disabled" {
		writeError(w, http.StatusOK, "状态筛选无效（可选 active / disabled）")
		return
	}

	res, err := h.deps.Store.ListUsers(
		strings.TrimSpace(r.URL.Query().Get("q")),
		status,
		normalizePage(queryInt(r, "page", 1)),
		normalizeSize(queryInt(r, "size", defaultPageSize)),
	)
	if err != nil {
		h.fail(w, "读取用户列表失败："+err.Error(), err)
		return
	}

	items := make([]siteUserView, 0, len(res.Items))
	for _, row := range res.Items {
		items = append(items, toSiteUserView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"pagination": pagination{
			Page:  res.Page,
			Size:  res.Size,
			Pages: res.Pages,
			Total: res.Total,
		},
	})
}

func (h *router) createUser(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Note     string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}

	username := strings.TrimSpace(req.Username)
	if !usernamePattern.MatchString(username) {
		writeError(w, http.StatusOK, "账号需 3-32 位字母、数字、下划线或短横线")
		return
	}
	if utf8.RuneCountInString(req.Password) < 6 {
		writeError(w, http.StatusOK, "密码至少 6 位")
		return
	}

	hash, err := userauth.HashPassword(req.Password)
	if err != nil {
		h.fail(w, "生成密码哈希失败："+err.Error(), err)
		return
	}
	note := strings.TrimSpace(req.Note)
	row, err := h.deps.Store.CreateUser(username, hash, note, nowString())
	if err != nil {
		// 数据层以普通 error 表达用户名唯一冲突，这里转成对前端友好的提示。
		if strings.Contains(err.Error(), "该账号已被注册") {
			writeError(w, http.StatusOK, "该账号已被注册")
			return
		}
		h.fail(w, "创建用户失败："+err.Error(), err)
		return
	}

	h.audit(admin, "user.create", "user", strconv.FormatInt(row.ID, 10), auditDetail(map[string]any{
		"username": row.Username,
		"status":   row.Status,
		"note":     row.Note,
	}))
	writeJSON(w, http.StatusOK, map[string]any{"user": toSiteUserView(row)})
}

func (h *router) updateUser(w http.ResponseWriter, r *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "用户标识无效")
		return
	}
	var req struct {
		Username *string `json:"username"`
		Status   *string `json:"status"`
		Note     *string `json:"note"`
		Password *string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if req.Username == nil && req.Status == nil && req.Note == nil && req.Password == nil {
		writeError(w, http.StatusOK, "没有需要保存的修改")
		return
	}

	var username *string
	if req.Username != nil {
		v := strings.TrimSpace(*req.Username)
		if !usernamePattern.MatchString(v) {
			writeError(w, http.StatusOK, "账号需 3-32 位字母、数字、下划线或短横线")
			return
		}
		username = &v
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "disabled" {
		writeError(w, http.StatusOK, "状态只能为 active 或 disabled")
		return
	}
	var note *string
	if req.Note != nil {
		v := strings.TrimSpace(*req.Note)
		note = &v
	}
	resetPassword := req.Password != nil && *req.Password != ""
	if resetPassword && utf8.RuneCountInString(*req.Password) < 6 {
		writeError(w, http.StatusOK, "密码至少 6 位")
		return
	}

	if username != nil || req.Status != nil || note != nil {
		if err := h.deps.Store.UpdateUser(id, username, req.Status, note, nowString()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusOK, "用户不存在")
				return
			}
			h.fail(w, "保存用户失败："+err.Error(), err)
			return
		}
		h.audit(admin, "user.update", "user", rawID, auditDetail(map[string]any{
			"username": req.Username,
			"status":   req.Status,
			"note":     req.Note,
		}))
	}

	if resetPassword {
		hash, err := userauth.HashPassword(*req.Password)
		if err != nil {
			h.fail(w, "生成密码哈希失败："+err.Error(), err)
			return
		}
		if err := h.deps.Store.UpdateUserPassword(id, hash); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusOK, "用户不存在")
				return
			}
			h.fail(w, "重置密码失败："+err.Error(), err)
			return
		}
		if err := h.deps.Store.DeleteUserSessions(id); err != nil {
			h.deps.Log.Printf("重置密码后清理用户会话失败 user=%d: %v", id, err)
		}
		h.audit(admin, "user.reset_password", "user", rawID, auditDetail(map[string]any{
			"username": username,
		}))
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) deleteUser(w http.ResponseWriter, _ *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "用户标识无效")
		return
	}
	if err := h.deps.Store.DeleteUser(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "用户不存在")
			return
		}
		h.fail(w, "删除用户失败："+err.Error(), err)
		return
	}
	h.audit(admin, "user.delete", "user", rawID, auditDetail(map[string]any{
		"id": id,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func toSiteUserView(row store.UserRow) siteUserView {
	return siteUserView{
		ID:          row.ID,
		Username:    row.Username,
		Status:      row.Status,
		Note:        row.Note,
		CreatedAt:   row.CreatedAt,
		LastLoginAt: row.LastLoginAt,
	}
}