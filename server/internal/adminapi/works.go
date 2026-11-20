package adminapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

func (h *router) listWorks(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "visible" && status != "hidden" {
		writeError(w, http.StatusOK, "状态筛选无效（可选 visible / hidden）")
		return
	}

	filter := store.WorksFilter{
		Q:            strings.TrimSpace(r.URL.Query().Get("q")),
		CategorySlug: strings.TrimSpace(r.URL.Query().Get("category")),
		Status:       status,
		Page:         normalizePage(queryInt(r, "page", 1)),
		Size:         normalizeSize(queryInt(r, "size", defaultPageSize)),
	}
	res, err := h.deps.Store.ListWorks(filter)
	if err != nil {
		h.fail(w, "读取影视库失败："+err.Error(), err)
		return
	}

	items := make([]workView, 0, len(res.Items))
	for _, row := range res.Items {
		items = append(items, toWorkView(row))
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

func (h *router) getWork(w http.ResponseWriter, _ *http.Request, _ store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "作品标识无效")
		return
	}
	detail, err := h.deps.Store.GetWork(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusOK, "作品不存在")
		return
	}
	if err != nil {
		h.fail(w, "读取作品失败："+err.Error(), err)
		return
	}

	files := make([]fileView, 0, len(detail.Files))
	for _, row := range detail.Files {
		files = append(files, toFileView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"work":  toWorkView(detail.Work),
		"files": files,
	})
}

func (h *router) patchWork(w http.ResponseWriter, r *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "作品标识无效")
		return
	}
	var req struct {
		Title      *string `json:"title"`
		CategoryID *int64  `json:"category_id"`
		Visible    *bool   `json:"visible"`
		Pinned     *bool   `json:"pinned"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if n := utf8.RuneCountInString(title); n < 1 || n > 120 {
			writeError(w, http.StatusOK, "标题长度需为 1-120 个字符")
			return
		}
		req.Title = &title
	}
	if req.CategoryID != nil && *req.CategoryID <= 0 {
		writeError(w, http.StatusOK, "分类标识无效")
		return
	}

	before, err := h.deps.Store.GetWork(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusOK, "作品不存在")
		return
	}
	if err != nil {
		h.fail(w, "读取作品失败："+err.Error(), err)
		return
	}

	if err := h.deps.Store.UpdateWork(id, store.WorkPatch{
		Title:      req.Title,
		CategoryID: req.CategoryID,
		Visible:    req.Visible,
		Pinned:     req.Pinned,
	}, nowString()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "作品不存在")
			return
		}
		h.fail(w, "保存作品失败："+err.Error(), err)
		return
	}

	beforeView := map[string]any{
		"title":       before.Work.Title,
		"category_id": before.Work.CategoryID,
		"visible":     before.Work.Visible,
		"pinned":      before.Work.Pinned,
	}
	afterView := map[string]any{
		"title":       before.Work.Title,
		"category_id": before.Work.CategoryID,
		"visible":     before.Work.Visible,
		"pinned":      before.Work.Pinned,
	}
	if req.Title != nil {
		afterView["title"] = *req.Title
	}
	if req.CategoryID != nil {
		afterView["category_id"] = *req.CategoryID
	}
	if req.Visible != nil {
		afterView["visible"] = *req.Visible
	}
	if req.Pinned != nil {
		afterView["pinned"] = *req.Pinned
	}
	h.audit(admin, "work.update", "work", rawID, auditDetail(map[string]any{
		"before": beforeView,
		"after":  afterView,
	}))

	after, err := h.deps.Store.GetWork(id)
	if err != nil {
		h.fail(w, "读取作品失败："+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"work": toWorkView(after.Work)})
}

func (h *router) setVisibility(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		IDs     []int64 `json:"ids"`
		Visible *bool   `json:"visible"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if req.Visible == nil {
		writeError(w, http.StatusOK, "请指定上架状态")
		return
	}
	if len(req.IDs) < 1 || len(req.IDs) > maxBulkIDs {
		writeError(w, http.StatusOK, "请选择 1-200 部作品")
		return
	}

	if err := h.deps.Store.SetVisibility(req.IDs, *req.Visible, nowString()); err != nil {
		h.fail(w, "批量上下架失败："+err.Error(), err)
		return
	}
	h.audit(admin, "work.visibility", "work", joinIDs(req.IDs), auditDetail(map[string]any{
		"visible": *req.Visible,
		"ids":     req.IDs,
	}))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(req.IDs)})
}

func (h *router) reorderEpisodes(w http.ResponseWriter, r *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "作品标识无效")
		return
	}
	var req struct {
		Order []int64 `json:"order"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if len(req.Order) < 1 || len(req.Order) > maxBulkIDs {
		writeError(w, http.StatusOK, "请提供 1-200 个文件标识")
		return
	}

	if err := h.deps.Store.ReorderEpisodes(id, req.Order, nowString()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "作品不存在")
			return
		}
		h.fail(w, "调整集序失败："+err.Error(), err)
		return
	}
	h.audit(admin, "work.episodes", "work", rawID, auditDetail(map[string]any{
		"order": req.Order,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) assignFiles(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		WorkID  int64   `json:"work_id"`
		FileIDs []int64 `json:"file_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if req.WorkID <= 0 {
		writeError(w, http.StatusOK, "作品标识无效")
		return
	}
	if len(req.FileIDs) < 1 || len(req.FileIDs) > maxBulkIDs {
		writeError(w, http.StatusOK, "请提供 1-200 个文件标识")
		return
	}

	if err := h.deps.Store.AssignFiles(req.WorkID, req.FileIDs, nowString()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "作品不存在")
			return
		}
		h.fail(w, "移动文件失败："+err.Error(), err)
		return
	}
	h.audit(admin, "work.assign", "work", strconv.FormatInt(req.WorkID, 10), auditDetail(map[string]any{
		"file_ids": req.FileIDs,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}