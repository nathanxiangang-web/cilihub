package adminapi

import (
	"errors"
	"net/http"

	"strings"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

type shareView struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	WorkID    int64  `json:"work_id"`
	WorkTitle string `json:"work_title"`
	Creator   string `json:"creator"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	Views     int64  `json:"views"`
	CreatedAt string `json:"created_at"`
}

func (h *router) listShares(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "active" && status != "revoked" && status != "expired" {
		writeError(w, http.StatusOK, "状态筛选无效（可选 active / revoked / expired）")
		return
	}

	res, err := h.deps.Store.ListShares(
		strings.TrimSpace(r.URL.Query().Get("q")),
		status,
		normalizePage(queryInt(r, "page", 1)),
		normalizeSize(queryInt(r, "size", defaultPageSize)),
	)
	if err != nil {
		h.fail(w, "读取分享列表失败："+err.Error(), err)
		return
	}

	items := make([]shareView, 0, len(res.Items))
	for _, row := range res.Items {
		items = append(items, toShareView(row))
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

func (h *router) revokeShare(w http.ResponseWriter, _ *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "分享标识无效")
		return
	}
	row, err := h.shareByID(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusOK, "分享不存在")
		return
	}
	if err != nil {
		h.fail(w, "读取分享失败："+err.Error(), err)
		return
	}
	if err := h.deps.Store.RevokeShare(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "分享不存在")
			return
		}
		h.fail(w, "取消分享失败："+err.Error(), err)
		return
	}
	h.audit(admin, "share.revoke", "share", rawID, auditDetail(map[string]any{
		"code": row.Code,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) restoreShare(w http.ResponseWriter, _ *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "分享标识无效")
		return
	}
	row, err := h.shareByID(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusOK, "分享不存在")
		return
	}
	if err != nil {
		h.fail(w, "读取分享失败："+err.Error(), err)
		return
	}
	if err := h.deps.Store.RestoreShare(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "分享不存在")
			return
		}
		h.fail(w, "恢复分享失败："+err.Error(), err)
		return
	}
	h.audit(admin, "share.restore", "share", rawID, auditDetail(map[string]any{
		"code": row.Code,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) deleteShare(w http.ResponseWriter, _ *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "分享标识无效")
		return
	}
	row, err := h.shareByID(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusOK, "分享不存在")
		return
	}
	if err != nil {
		h.fail(w, "读取分享失败："+err.Error(), err)
		return
	}
	if err := h.deps.Store.DeleteShare(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "分享不存在")
			return
		}
		h.fail(w, "删除分享失败："+err.Error(), err)
		return
	}
	h.audit(admin, "share.delete", "share", rawID, auditDetail(map[string]any{
		"code": row.Code,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// shareByID 用于让审计 detail 带上分享码：C-SHARE@1 只提供按 code 查询，
// 因此逐页扫描列表。后台分享量级小且写操作低频，这一点开销可以接受。
func (h *router) shareByID(id int64) (store.ShareRow, error) {
	for page := 1; page <= maxListPages; page++ {
		res, err := h.deps.Store.ListShares("", "", page, maxPageSize)
		if err != nil {
			return store.ShareRow{}, err
		}
		for _, row := range res.Items {
			if row.ID == id {
				return row, nil
			}
		}
		if len(res.Items) == 0 || page >= res.Pages {
			break
		}
	}
	return store.ShareRow{}, store.ErrNotFound
}

func toShareView(row store.ShareRow) shareView {
	return shareView{
		ID:        row.ID,
		Code:      row.Code,
		WorkID:    row.WorkID,
		WorkTitle: row.WorkTitle,
		Creator:   row.Creator,
		Status:    row.Status,
		ExpiresAt: row.ExpiresAt,
		Views:     row.Views,
		CreatedAt: row.CreatedAt,
	}
}