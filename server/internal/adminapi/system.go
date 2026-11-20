package adminapi

import (
	"net/http"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

type overviewLastSync struct {
	Status  string `json:"status"`
	At      string `json:"at"`
	Added   int    `json:"added"`
	Missing int    `json:"missing"`
}

func (h *router) overview(w http.ResponseWriter, _ *http.Request, _ store.AdminUser) {
	items, err := h.allWorks()
	if err != nil {
		h.fail(w, "统计影视库失败："+err.Error(), err)
		return
	}
	visible := 0
	missing := 0
	for _, row := range items {
		if row.Visible {
			visible++
		}
		missing += row.MissingCount
	}

	state, err := h.deps.Store.SyncState()
	if err != nil {
		h.fail(w, "读取同步状态失败："+err.Error(), err)
		return
	}
	var lastSync any
	if state.Last != nil {
		lastSync = overviewLastSync{
			Status:  state.Last.Status,
			At:      atOf(*state.Last),
			Added:   state.Last.Added,
			Missing: state.Last.Missing,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"works":     len(items),
		"visible":   visible,
		"missing":   missing,
		"last_sync": lastSync,
	})
}

func (h *router) syncState(w http.ResponseWriter, _ *http.Request, _ store.AdminUser) {
	state, err := h.deps.Store.SyncState()
	if err != nil {
		h.fail(w, "读取同步状态失败："+err.Error(), err)
		return
	}

	runs := make([]runView, 0, len(state.Runs))
	for _, run := range state.Runs {
		runs = append(runs, toRunView(run))
	}
	events := make([]eventView, 0, len(state.Events))
	for _, ev := range state.Events {
		events = append(events, toEventView(ev))
	}
	var last any
	if state.Last != nil {
		last = toRunView(*state.Last)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"last":   last,
		"runs":   runs,
		"events": events,
	})
}

func (h *router) runSync(w http.ResponseWriter, _ *http.Request, _ store.AdminUser) {
	if h.deps.Catalog == nil {
		writeError(w, http.StatusOK, "未配置 OpenList 目录源")
		return
	}
	runID, err := h.deps.Catalog.SyncNow()
	if err != nil {
		h.fail(w, "触发同步失败："+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID})
}

func (h *router) diagnostics(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	fileID, ok := parseID(r.URL.Query().Get("file_id"))
	if !ok {
		writeError(w, http.StatusOK, "文件标识无效")
		return
	}
	if h.deps.Catalog == nil {
		writeError(w, http.StatusOK, "未配置 OpenList 目录源")
		return
	}
	diag, err := h.deps.Catalog.Diagnose(r.Context(), fileID)
	if err != nil {
		h.fail(w, "播放诊断失败："+err.Error(), err)
		return
	}
	writeJSON(w, http.StatusOK, diag)
}

func (h *router) auditList(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	page := normalizePage(queryInt(r, "page", 1))
	size := queryInt(r, "size", auditPageSize)
	if size < 1 {
		size = auditPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	res, err := h.deps.Store.AuditList(page, size)
	if err != nil {
		h.fail(w, "读取审计日志失败："+err.Error(), err)
		return
	}
	items := make([]auditView, 0, len(res.Items))
	for _, row := range res.Items {
		items = append(items, toAuditView(row))
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