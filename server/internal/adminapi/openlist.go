package adminapi

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/nathanxiangang-web/cilihub/server/internal/filmcatalog"
	"github.com/nathanxiangang-web/cilihub/server/internal/secret"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

type openlistView struct {
	Configured  bool   `json:"configured"`
	Origin      string `json:"origin"`
	Folder      string `json:"folder"`
	CDNHost     string `json:"cdn_host"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	UpdatedAt   string `json:"updated_at"`
}

type openlistTestView struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Entries   int    `json:"entries"`
	Message   string `json:"message"`
}

// openlistState 返回当前连接配置；密码只以 password_set 布尔展示。
func (h *router) openlistState(w http.ResponseWriter, _ *http.Request, _ store.AdminUser) {
	row, err := h.deps.Store.OpenListSettings()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.fail(w, "读取 OpenList 配置失败："+err.Error(), err)
		return
	}

	view := openlistView{}
	if err == nil {
		view.Origin = row.Origin
		view.Folder = row.Folder
		view.CDNHost = row.CDNHost
		view.Username = row.Username
		view.PasswordSet = row.PasswordEnc != ""
		view.UpdatedAt = row.UpdatedAt
	}
	if h.deps.Catalog != nil {
		view.Configured = h.deps.Catalog.Configured()
	} else {
		view.Configured = err == nil
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *router) openlistTest(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	var req struct {
		Origin   string `json:"origin"`
		Username string `json:"username"`
		Password string `json:"password"`
		Folder   string `json:"folder"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}

	origin := strings.TrimSpace(req.Origin)
	username := strings.TrimSpace(req.Username)
	folder := strings.TrimSpace(req.Folder)
	if err := validateOpenListTarget(origin, username, folder); err != nil {
		writeError(w, http.StatusOK, err.Error())
		return
	}

	cfg := filmcatalog.ListConfig{Origin: origin, Folder: folder, Username: username, Password: req.Password}
	res, err := filmcatalog.TestConnection(r.Context(), cfg)
	if err != nil {
		res.OK = false
		if res.Message == "" {
			res.Message = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, openlistTestView{
		OK:        res.OK,
		Status:    res.Status,
		ElapsedMS: res.ElapsedMS,
		Entries:   res.Entries,
		Message:   res.Message,
	})
}

type openlistBrowseItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// openlistBrowse 只读浏览 OpenList 目录树，供目录选择器使用；不修改任何配置。
func (h *router) openlistBrowse(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	if h.deps.Catalog == nil {
		writeError(w, http.StatusOK, "尚未配置 OpenList 连接")
		return
	}
	dir, dirs, err := h.deps.Catalog.BrowseDirs(r.Context(), strings.TrimSpace(r.URL.Query().Get("path")))
	if err != nil {
		writeError(w, http.StatusOK, err.Error())
		return
	}
	items := make([]openlistBrowseItem, 0, len(dirs))
	for _, item := range dirs {
		items = append(items, openlistBrowseItem{Name: item.Name, Path: item.Path})
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "items": items})
}

// openlistSave 校验后加密入库，再把适配器指向新配置；Apply 失败时回退旧配置。
func (h *router) openlistSave(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		Origin   string `json:"origin"`
		Username string `json:"username"`
		Password string `json:"password"`
		Folder   string `json:"folder"`
		CDNHost  string `json:"cdn_host"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}

	origin := strings.TrimSpace(req.Origin)
	username := strings.TrimSpace(req.Username)
	folder := strings.TrimSpace(req.Folder)
	cdnHost := strings.TrimSpace(req.CDNHost)
	if err := validateOpenListTarget(origin, username, folder); err != nil {
		writeError(w, http.StatusOK, err.Error())
		return
	}
	if err := validateCDNHost(cdnHost); err != nil {
		writeError(w, http.StatusOK, err.Error())
		return
	}
	if h.deps.Catalog == nil {
		writeError(w, http.StatusOK, "OpenList 适配器不可用，请检查服务配置")
		return
	}

	old, oldErr := h.deps.Store.OpenListSettings()
	hasOld := oldErr == nil
	if oldErr != nil && !errors.Is(oldErr, store.ErrNotFound) {
		h.fail(w, "读取 OpenList 配置失败："+oldErr.Error(), oldErr)
		return
	}

	password := req.Password
	if password == "" {
		if !hasOld {
			writeError(w, http.StatusOK, "首次配置需填写 OpenList 密码")
			return
		}
		plain, err := secret.Open(old.PasswordEnc)
		if err != nil {
			h.fail(w, "读取已保存的 OpenList 密码失败："+err.Error(), err)
			return
		}
		password = plain
	}

	enc, err := secret.Seal(password)
	if err != nil {
		h.fail(w, "加密 OpenList 密码失败："+err.Error(), err)
		return
	}

	now := nowString()
	next := store.OpenListSettings{
		Origin:      origin,
		Folder:      folder,
		CDNHost:     cdnHost,
		Username:    username,
		PasswordEnc: enc,
		UpdatedAt:   now,
	}
	if err := h.deps.Store.SaveOpenListSettings(next, now); err != nil {
		h.fail(w, "保存 OpenList 配置失败："+err.Error(), err)
		return
	}

	cfg := filmcatalog.ListConfig{Origin: origin, Folder: folder, CDNHost: cdnHost, Username: username, Password: password}
	if err := h.deps.Catalog.Apply(cfg, now); err != nil {
		if hasOld {
			h.rollbackOpenList(old)
		}
		h.fail(w, "应用新配置失败："+err.Error(), err)
		return
	}

	h.audit(admin, "openlist.update", "openlist", "1", auditDetail(map[string]any{
		"origin":   origin,
		"folder":   folder,
		"cdn_host": cdnHost,
		"username": username,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) rollbackOpenList(old store.OpenListSettings) {
	if err := h.deps.Store.SaveOpenListSettings(old, old.UpdatedAt); err != nil {
		h.deps.Log.Printf("回滚 OpenList 配置失败: %v", err)
		return
	}
	plain, err := secret.Open(old.PasswordEnc)
	if err != nil {
		h.deps.Log.Printf("回滚 OpenList 连接失败（读取旧密码）: %v", err)
		return
	}
	cfg := filmcatalog.ListConfig{
		Origin:   old.Origin,
		Folder:   old.Folder,
		CDNHost:  old.CDNHost,
		Username: old.Username,
		Password: plain,
	}
	if err := h.deps.Catalog.Apply(cfg, old.UpdatedAt); err != nil {
		h.deps.Log.Printf("回滚 OpenList 连接失败: %v", err)
	}
}

func validateOpenListTarget(origin, username, folder string) error {
	if username == "" {
		return errors.New("请填写 OpenList 账号")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("OpenList 地址需为 https:// 开头的主机地址，不能包含账号、路径、查询或片段")
	}
	if folder == "" || !strings.HasPrefix(folder, "/") || path.Clean(folder) != folder ||
		folder == "/" || strings.Contains(folder, `\`) {
		return errors.New("映射目录需为 / 开头的绝对路径，不能是根目录，也不能包含反斜杠")
	}
	return nil
}

func validateCDNHost(cdnHost string) error {
	if strings.ContainsAny(cdnHost, ":/@?#\\ ") {
		return errors.New("CDN 主机名只能是主机名，不能包含协议、路径、空格或特殊符号")
	}
	return nil
}