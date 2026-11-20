package adminapi

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

func (h *router) listCategories(w http.ResponseWriter, _ *http.Request, _ store.AdminUser) {
	rows, err := h.deps.Store.Categories()
	if err != nil {
		h.fail(w, "读取分类失败："+err.Error(), err)
		return
	}
	items := make([]categoryView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toCategoryView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *router) createCategory(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 120 {
		writeError(w, http.StatusOK, "分类名称长度需为 1-120 个字符")
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if !slugPattern.MatchString(slug) {
		writeError(w, http.StatusOK, "分类标识只能包含小写字母、数字与短横线，长度 1-40")
		return
	}

	row, err := h.deps.Store.CreateCategory(name, slug)
	if err != nil {
		h.fail(w, "创建分类失败："+err.Error(), err)
		return
	}
	h.audit(admin, "category.create", "category", strconv.FormatInt(row.ID, 10), auditDetail(map[string]any{
		"name": row.Name,
		"slug": row.Slug,
	}))
	writeJSON(w, http.StatusOK, map[string]any{"category": toCategoryView(row)})
}

func (h *router) updateCategory(w http.ResponseWriter, r *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "分类标识无效")
		return
	}
	var req struct {
		Name    string `json:"name"`
		Visible *bool  `json:"visible"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 120 {
		writeError(w, http.StatusOK, "分类名称长度需为 1-120 个字符")
		return
	}

	current, err := h.deps.Store.Categories()
	if err != nil {
		h.fail(w, "读取分类失败："+err.Error(), err)
		return
	}
	visible := false
	found := false
	for _, row := range current {
		if row.ID == id {
			visible = row.Visible
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusOK, "分类不存在")
		return
	}
	if req.Visible != nil {
		visible = *req.Visible
	}

	if err := h.deps.Store.UpdateCategory(id, name, visible); err != nil {
		h.fail(w, "保存分类失败："+err.Error(), err)
		return
	}
	h.audit(admin, "category.update", "category", rawID, auditDetail(map[string]any{
		"name":    name,
		"visible": visible,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) reorderCategories(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if len(req.IDs) < 1 || len(req.IDs) > maxBulkIDs {
		writeError(w, http.StatusOK, "请提供 1-200 个分类标识")
		return
	}

	if err := h.deps.Store.ReorderCategories(req.IDs); err != nil {
		h.fail(w, "调整分类顺序失败："+err.Error(), err)
		return
	}
	h.audit(admin, "category.reorder", "category", joinIDs(req.IDs), auditDetail(map[string]any{
		"ids": req.IDs,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) getHome(w http.ResponseWriter, _ *http.Request, _ store.AdminUser) {
	items, err := h.allWorks()
	if err != nil {
		h.fail(w, "读取首页编排失败："+err.Error(), err)
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SortIndex != items[j].SortIndex {
			return items[i].SortIndex < items[j].SortIndex
		}
		return items[i].ID < items[j].ID
	})

	pinned := make([]int64, 0, len(items))
	order := make([]int64, 0, len(items))
	for _, row := range items {
		if row.Pinned {
			pinned = append(pinned, row.ID)
		}
		order = append(order, row.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pinned": pinned, "order": order})
}

func (h *router) setHome(w http.ResponseWriter, r *http.Request, admin store.AdminUser) {
	var req struct {
		Pinned []int64 `json:"pinned"`
		Order  []int64 `json:"order"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	if len(req.Pinned) > maxBulkIDs || len(req.Order) > maxBulkIDs {
		writeError(w, http.StatusOK, "首页编排数量不能超过 200")
		return
	}

	if err := h.deps.Store.SetHomeOrder(req.Pinned, req.Order, nowString()); err != nil {
		h.fail(w, "保存首页编排失败："+err.Error(), err)
		return
	}
	h.audit(admin, "home.order", "home", "", auditDetail(map[string]any{
		"pinned": req.Pinned,
		"order":  req.Order,
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}