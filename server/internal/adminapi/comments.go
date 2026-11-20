package adminapi

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

const commentExcerptRunes = 60

type commentView struct {
	ID        int64  `json:"id"`
	WorkID    int64  `json:"work_id"`
	WorkTitle string `json:"work_title"`
	User      string `json:"user"`
	EpisodeNo int    `json:"episode_no"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func (h *router) listComments(w http.ResponseWriter, r *http.Request, _ store.AdminUser) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "visible" && status != "hidden" {
		writeError(w, http.StatusOK, "状态筛选无效（可选 visible / hidden）")
		return
	}

	res, err := h.deps.Store.AdminListComments(
		strings.TrimSpace(r.URL.Query().Get("q")),
		status,
		normalizePage(queryInt(r, "page", 1)),
		normalizeSize(queryInt(r, "size", defaultPageSize)),
	)
	if err != nil {
		h.fail(w, "读取评论列表失败："+err.Error(), err)
		return
	}

	titles := map[int64]string(nil)
	if len(res.Items) > 0 {
		if titles, err = h.workTitles(); err != nil {
			h.fail(w, "读取作品标题失败："+err.Error(), err)
			return
		}
	}

	items := make([]commentView, 0, len(res.Items))
	for _, row := range res.Items {
		items = append(items, toCommentView(row, titles[row.WorkID]))
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

func (h *router) updateComment(w http.ResponseWriter, r *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "评论标识无效")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusOK, "请求参数无效")
		return
	}
	status := strings.TrimSpace(req.Status)
	if status != "visible" && status != "hidden" {
		writeError(w, http.StatusOK, "状态只能为 visible 或 hidden")
		return
	}

	row, err := h.commentByID(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusOK, "评论不存在")
		return
	}
	if err != nil {
		h.fail(w, "读取评论失败："+err.Error(), err)
		return
	}

	if err := h.deps.Store.SetCommentStatus(id, status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "评论不存在")
			return
		}
		h.fail(w, "更新评论状态失败："+err.Error(), err)
		return
	}

	action := "comment.show"
	if status == "hidden" {
		action = "comment.hide"
	}
	h.audit(admin, action, "comment", rawID, auditDetail(map[string]any{
		"work_id": row.WorkID,
		"excerpt": commentExcerpt(row.Content),
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *router) deleteComment(w http.ResponseWriter, _ *http.Request, admin store.AdminUser, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(w, http.StatusOK, "评论标识无效")
		return
	}

	row, err := h.commentByID(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusOK, "评论不存在")
		return
	}
	if err != nil {
		h.fail(w, "读取评论失败："+err.Error(), err)
		return
	}

	if err := h.deps.Store.DeleteComment(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusOK, "评论不存在")
			return
		}
		h.fail(w, "删除评论失败："+err.Error(), err)
		return
	}

	h.audit(admin, "comment.delete", "comment", rawID, auditDetail(map[string]any{
		"work_id": row.WorkID,
		"excerpt": commentExcerpt(row.Content),
	}))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// commentByID 用于让审计 detail 带上作品与内容摘要：C-INTERACT@1 没有单条读取接口，
// 因此逐页扫描后台列表。C-INTERACT@1 未明确 AdminListComments 的空状态是否等于全量，
// 故先按空状态扫描，再补一次 hidden，确保隐藏评论也能拿到审计信息。
func (h *router) commentByID(id int64) (store.CommentRow, error) {
	for _, status := range []string{"", "hidden"} {
		for page := 1; page <= maxListPages; page++ {
			res, err := h.deps.Store.AdminListComments("", status, page, maxPageSize)
			if err != nil {
				return store.CommentRow{}, err
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
	}
	return store.CommentRow{}, store.ErrNotFound
}

func commentExcerpt(content string) string {
	content = strings.TrimSpace(content)
	if utf8.RuneCountInString(content) <= commentExcerptRunes {
		return content
	}
	return string([]rune(content)[:commentExcerptRunes]) + "…"
}

// workTitles 一次性构建 id→标题映射：C-INTERACT@1 的 CommentRow 未暴露作品标题字段，
// 后台列表页数据量小，逐页读取作品的成本可以接受。
func (h *router) workTitles() (map[int64]string, error) {
	works, err := h.allWorks()
	if err != nil {
		return nil, err
	}
	titles := make(map[int64]string, len(works))
	for _, row := range works {
		titles[row.ID] = row.Title
	}
	return titles, nil
}

func toCommentView(row store.CommentRow, workTitle string) commentView {
	return commentView{
		ID:        row.ID,
		WorkID:    row.WorkID,
		WorkTitle: workTitle,
		User:      row.Username,
		EpisodeNo: row.EpisodeNo,
		Content:   row.Content,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
	}
}