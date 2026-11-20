package commands

import (
	"errors"

	"github.com/nathanxiangang-web/cilihub/server/internal/shares"
)

func shareRoutes() map[string]command {
	return map[string]command{
		"api_share_create": apiShareCreate,
		"api_share_info":   apiShareInfo,
		"api_share_unlock": apiShareUnlock,
	}
}

func (h *router) shareService() (*shares.Service, error) {
	if h.deps.Shares == nil {
		return nil, errors.New("分享服务未启用")
	}
	return h.deps.Shares, nil
}

func apiShareCreate(c *call) (any, error) {
	user, loggedIn, err := c.h.currentUser(c.r)
	if err != nil {
		return nil, errors.New("读取账号失败：" + err.Error())
	}
	if !loggedIn {
		return nil, errors.New("请先登录")
	}
	svc, err := c.h.shareService()
	if err != nil {
		return nil, err
	}
	row, err := svc.Create(c.r.Context(), user,
		c.p.i64(0, "work_id", "workId"),
		int(c.p.i64(0, "episode_no", "episodeNo")),
		c.p.str("password"),
		int(c.p.i64(0, "expires_days", "expiresDays")))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"code":       row.Code,
		"url":        "/s/" + row.Code,
		"expires_at": row.ExpiresAt,
	}, nil
}

func apiShareInfo(c *call) (any, error) {
	svc, err := c.h.shareService()
	if err != nil {
		return nil, err
	}
	info, err := svc.Info(c.r.Context(), c.p.str("code"))
	if err != nil {
		return nil, errors.New("读取分享失败：" + err.Error())
	}
	return map[string]any{
		"title":          info.Title,
		"episode_count":  info.EpisodeCount,
		"need_password":  info.NeedPassword,
		"state":          info.State,
	}, nil
}

func apiShareUnlock(c *call) (any, error) {
	svc, err := c.h.shareService()
	if err != nil {
		return nil, err
	}
	if err := svc.Unlock(c.p.str("code"), c.p.str("password")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}