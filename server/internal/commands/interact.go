package commands

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

const (
	maxCommentRunes = 500
	defaultPageSize = 20
)

// interactRoutes 覆盖 contentRoutes 中同名的边缘转发命令，改为本地落库。
func interactRoutes() map[string]command {
	return map[string]command{
		"api_like":         apiLikeLocal,
		"api_collect":      apiCollectLocal,
		"api_comments":     apiCommentsLocal,
		"api_comment_send": apiCommentSendLocal,
		"api_follow":       apiFollowLocal,
		"api_my_likes":     apiMyLikes,
		"api_my_favs":      apiMyFavs,
	}
}

func (h *router) interactiveUser(r *http.Request) (store.UserRow, error) {
	row, loggedIn, err := h.currentUser(r)
	if err != nil {
		return store.UserRow{}, errors.New("读取账号失败：" + err.Error())
	}
	if !loggedIn {
		return store.UserRow{}, errors.New("请先登录")
	}
	return row, nil
}

func apiLikeLocal(c *call) (any, error) {
	user, err := c.h.interactiveUser(c.r)
	if err != nil {
		return nil, err
	}
	id := c.p.i64(0, "id")
	if id <= 0 {
		return nil, errors.New("视频不存在")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := c.h.deps.Store.SetLike(user.ID, id, c.p.flag("on"), now); err != nil {
		return nil, errors.New("保存点赞失败：" + err.Error())
	}
	counts, err := c.h.deps.Store.WorkCounts(id)
	if err != nil {
		return nil, errors.New("读取点赞数失败：" + err.Error())
	}
	return map[string]any{"ok": true, "count": counts.Likes}, nil
}

func apiCollectLocal(c *call) (any, error) {
	user, err := c.h.interactiveUser(c.r)
	if err != nil {
		return nil, err
	}
	id := c.p.i64(0, "id")
	if id <= 0 {
		return nil, errors.New("视频不存在")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := c.h.deps.Store.SetFavorite(user.ID, id, c.p.flag("on"), now); err != nil {
		return nil, errors.New("保存收藏失败：" + err.Error())
	}
	counts, err := c.h.deps.Store.WorkCounts(id)
	if err != nil {
		return nil, errors.New("读取收藏数失败：" + err.Error())
	}
	return map[string]any{"ok": true, "count": counts.Favorites}, nil
}

func apiCommentsLocal(c *call) (any, error) {
	id := c.p.i64(0, "id")
	if id <= 0 {
		return nil, errors.New("视频不存在")
	}
	page, size := pageArgs(c)
	list, err := c.h.deps.Store.ListComments(id, "", page, size)
	if err != nil {
		return nil, errors.New("读取评论失败：" + err.Error())
	}
	items := make([]map[string]any, 0, len(list.Items))
	for _, row := range list.Items {
		items = append(items, map[string]any{
			"id":         row.ID,
			"user":       map[string]any{"id": row.UserID, "name": row.Username},
			"content":    row.Content,
			"created_at": row.CreatedAt,
			"ep":         row.EpisodeNo,
		})
	}
	return map[string]any{
		"items": items,
		"pagination": map[string]int{
			"page": list.Page, "size": list.Size, "pages": list.Pages, "total": list.Total,
		},
	}, nil
}

func apiCommentSendLocal(c *call) (any, error) {
	user, err := c.h.interactiveUser(c.r)
	if err != nil {
		return nil, err
	}
	id := c.p.i64(0, "id")
	if id <= 0 {
		return nil, errors.New("视频不存在")
	}
	text := strings.TrimSpace(c.p.str("text"))
	length := utf8.RuneCountInString(text)
	if length == 0 || length > maxCommentRunes {
		return nil, errors.New("评论内容需在 1-500 字之间")
	}
	ep := int(c.p.i64(1, "ep"))
	if ep < 1 {
		ep = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := c.h.deps.Store.AddComment(id, ep, user.ID, text, now); err != nil {
		return nil, errors.New("发表评论失败：" + err.Error())
	}
	counts, err := c.h.deps.Store.WorkCounts(id)
	if err != nil {
		return nil, errors.New("读取评论数失败：" + err.Error())
	}
	return map[string]any{"ok": true, "count": counts.Comments}, nil
}

// apiFollowLocal 保留前端关注调用，暂不落库。
func apiFollowLocal(c *call) (any, error) {
	return map[string]any{"ok": true}, nil
}

func apiMyLikes(c *call) (any, error) {
	user, err := c.h.interactiveUser(c.r)
	if err != nil {
		return nil, err
	}
	items, err := c.h.deps.Store.UserLikes(user.ID)
	if err != nil {
		return nil, errors.New("读取点赞失败：" + err.Error())
	}
	if items == nil {
		items = []int64{}
	}
	return map[string]any{"items": items}, nil
}

func apiMyFavs(c *call) (any, error) {
	user, err := c.h.interactiveUser(c.r)
	if err != nil {
		return nil, err
	}
	items, err := c.h.deps.Store.UserFavorites(user.ID)
	if err != nil {
		return nil, errors.New("读取收藏失败：" + err.Error())
	}
	if items == nil {
		items = []int64{}
	}
	return map[string]any{"items": items}, nil
}

func pageArgs(c *call) (int, int) {
	page := int(c.p.i64(1, "page"))
	size := int(c.p.i64(defaultPageSize, "size"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = defaultPageSize
	}
	return page, size
}