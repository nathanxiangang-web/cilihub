package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func contentRoutes() map[string]command {
	return map[string]command{
		"api_feed":           apiFeed,
		"api_category":       apiCategory,
		"api_rank":           apiRank,
		"api_search":         apiSearch,
		"api_posts":          apiPosts,
		"api_post":           apiPost,
		"api_post_like":      apiPostLike,
		"api_post_collect":   apiPostCollect,
		"api_post_comments":  apiPostComments,
		"api_video":          apiVideo,
		"api_play":           apiPlay,
		"api_comments":       apiComments,
		"api_comment_send":   apiCommentSend,
		"api_like":           apiLike,
		"api_collect":        apiCollect,
		"api_follow":         apiFollow,
	}
}

func apiFeed(c *call) (any, error) {
	page := c.p.i64(1, "page")
	size := c.p.i64(20, "size")
	return c.h.edge(c.r.Context(), "GET", fmt.Sprintf("/api/videos?page=%d&size=%d", page, size), nil, c.h.token(c.r))
}

func apiCategory(c *call) (any, error) {
	page := c.p.i64(1, "page")
	size := c.p.i64(20, "size")
	return c.h.edge(c.r.Context(), "GET", fmt.Sprintf("/api/videos/category/%s?page=%d&size=%d", c.p.str("cate"), page, size), nil, c.h.token(c.r))
}

func apiRank(c *call) (any, error) {
	kind := c.p.str("kind")
	if kind != "recommend" && kind != "potential" {
		kind = "hot"
	}
	return c.h.edge(c.r.Context(), "GET", fmt.Sprintf("/api/ranks/%s", kind), nil, c.h.token(c.r))
}

func apiSearch(c *call) (any, error) {
	encoded := strings.ReplaceAll(url.QueryEscape(c.p.str("q")), "+", "%20")
	return c.h.edge(c.r.Context(), "GET", fmt.Sprintf("/api/search?q=%s&page=%d&size=%d", encoded, c.p.i64(1, "page"), c.p.i64(20, "size")), nil, c.h.token(c.r))
}

func apiPosts(c *call) (any, error) {
	page := c.p.i64(1, "page")
	size := c.p.i64(20, "size")
	return c.h.edge(c.r.Context(), "GET", fmt.Sprintf("/api/posts?page=%d&size=%d", page, size), nil, c.h.token(c.r))
}

func apiPost(c *call) (any, error) {
	return c.h.edge(c.r.Context(), "GET", fmt.Sprintf("/api/posts/%d", c.p.i64(0, "id")), nil, c.h.token(c.r))
}

func apiPostLike(c *call) (any, error) {
	token, err := c.h.authed(c.r)
	if err != nil {
		return nil, err
	}
	method := "POST"
	if !c.p.flag("on") {
		method = "DELETE"
	}
	return c.h.edge(c.r.Context(), method, fmt.Sprintf("/api/posts/%d/like", c.p.i64(0, "id")), nil, token)
}

func apiPostCollect(c *call) (any, error) {
	token, err := c.h.authed(c.r)
	if err != nil {
		return nil, err
	}
	method := "POST"
	if !c.p.flag("on") {
		method = "DELETE"
	}
	return c.h.edge(c.r.Context(), method, fmt.Sprintf("/api/posts/%d/collect", c.p.i64(0, "id")), nil, token)
}

func apiPostComments(c *call) (any, error) {
	path := fmt.Sprintf("/api/posts/%d/comments?page=%d&size=%d", c.p.i64(0, "id"), c.p.i64(1, "page"), c.p.i64(20, "size"))
	return c.h.edge(c.r.Context(), "GET", path, nil, c.h.token(c.r))
}

func apiVideo(c *call) (any, error) {
	return c.h.edge(c.r.Context(), "GET", fmt.Sprintf("/api/videos/%d", c.p.i64(0, "id")), nil, c.h.token(c.r))
}

func apiPlay(c *call) (any, error) {
	ctx := c.r.Context()
	id := c.p.i64(0, "id")
	ep := c.p.i64(1, "ep")
	token := c.h.token(c.r)

	primary, primaryErr := c.h.edge(ctx, "POST", fmt.Sprintf("/api/videos/%d/episodes/%d/playback", id, ep), map[string]any{}, token)
	var primaryInfo map[string]any
	if primaryErr == nil {
		primaryInfo = decodeObject(primary)
		normalizePlayInfo(primaryInfo)
		if hasPlayURL(primaryInfo) {
			return primaryInfo, nil
		}
	}

	legacy, legacyErr := c.h.edge(ctx, "GET", fmt.Sprintf("/api/videos/%d/play?ep=%d&page_texts=0", id, ep), nil, token)
	if legacyErr == nil {
		legacyInfo := decodeObject(legacy)
		normalizePlayInfo(legacyInfo)
		if hasPlayURL(legacyInfo) || primaryErr != nil {
			return legacyInfo, nil
		}
	}
	if primaryErr != nil {
		return nil, primaryErr
	}
	return primaryInfo, nil
}

func apiComments(c *call) (any, error) {
	path := fmt.Sprintf("/api/videos/%d/comments?ep=%d&page=%d&size=%d",
		c.p.i64(0, "id"), c.p.i64(1, "ep"), c.p.i64(1, "page"), c.p.i64(20, "size"))
	return c.h.edge(c.r.Context(), "GET", path, nil, c.h.token(c.r))
}

func apiCommentSend(c *call) (any, error) {
	token, err := c.h.authed(c.r)
	if err != nil {
		return nil, err
	}
	id := c.p.i64(0, "id")
	replyID := c.p.i64(0, "reply_id", "replyId")
	body := map[string]any{"text": c.p.str("text")}
	path := fmt.Sprintf("/api/videos/%d/comments?ep=%d", id, c.p.i64(1, "ep"))
	if replyID > 0 {
		body["reply_id"] = replyID
		path = fmt.Sprintf("/api/videos/%d/comments", id)
	}
	return c.h.edge(c.r.Context(), "POST", path, body, token)
}

func apiLike(c *call) (any, error) {
	token, err := c.h.authed(c.r)
	if err != nil {
		return nil, err
	}
	method := "POST"
	if !c.p.flag("on") {
		method = "DELETE"
	}
	return c.h.edge(c.r.Context(), method, fmt.Sprintf("/api/videos/%d/like?ep=%d", c.p.i64(0, "id"), c.p.i64(1, "ep")), nil, token)
}

func apiCollect(c *call) (any, error) {
	token, err := c.h.authed(c.r)
	if err != nil {
		return nil, err
	}
	method := "POST"
	if !c.p.flag("on") {
		method = "DELETE"
	}
	return c.h.edge(c.r.Context(), method, fmt.Sprintf("/api/videos/%d/collect", c.p.i64(0, "id")), nil, token)
}

func apiFollow(c *call) (any, error) {
	token, err := c.h.authed(c.r)
	if err != nil {
		return nil, err
	}
	method := "POST"
	if !c.p.flag("on") {
		method = "DELETE"
	}
	return c.h.edge(c.r.Context(), method, fmt.Sprintf("/api/users/%d/follow", c.p.i64(0, "uid")), nil, token)
}

func decodeObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil
	}
	return obj
}

// normalizePlayInfo 把新版 streams.{main,hevc} 回填到旧的 video_url 字段。
func normalizePlayInfo(info map[string]any) {
	if info == nil {
		return
	}
	streams, _ := info["streams"].(map[string]any)
	if streams == nil {
		return
	}
	fill := func(field string, stream any) {
		value, _ := stream.(string)
		if value == "" {
			return
		}
		if current, _ := info[field].(string); current == "" {
			info[field] = value
		}
	}
	fill("video_url", streams["main"])
	fill("video_url_hevc", streams["hevc"])
}

func hasPlayURL(info map[string]any) bool {
	value, _ := info["video_url"].(string)
	return value != ""
}