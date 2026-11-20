package commands

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/nathanxiangang-web/cilihub/server/internal/session"
)

func authRoutes() map[string]command {
	return map[string]command{
		"app_boot":     appBoot,
		"security_set": securitySet,
		"api_login":    apiLogin,
		"api_register": apiRegister,
		"api_logout":   apiLogout,
		"api_me":       apiMe,
	}
}

func appBoot(c *call) (any, error) {
	token := c.h.token(c.r)
	user := json.RawMessage("null")
	if token != "" {
		if raw, err := c.h.edge(c.r.Context(), "GET", "/api/account/me", nil, token); err == nil {
			user = raw
		}
	}
	return map[string]any{
		"user":            user,
		"logged_in":       token != "",
		"base":            c.h.deps.Base(),
		"strict_security": true,
		"cli_relaxed":     false,
	}, nil
}

// securitySet：服务端固定严格模式，浏览器端无法放宽。
func securitySet(c *call) (any, error) {
	return true, nil
}

func apiLogin(c *call) (any, error) {
	origin := c.h.deps.Base()
	raw, err := c.h.edge(c.r.Context(), "POST", "/api/account/loginByPassword", map[string]any{
		"username": c.p.str("username"),
		"password": c.p.str("password"),
	}, "")
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, errors.New("登录返回缺少 token")
	}
	token, ok := rawString(fields["token"])
	if !ok {
		return nil, errors.New("登录返回缺少 token")
	}
	if c.h.deps.Base() != origin {
		return nil, errors.New("登录期间线路已变化，请重新登录")
	}
	session.Set(c.w, c.r, token)
	user := json.RawMessage("null")
	if value, ok := fields["user"]; ok {
		user = value
	}
	return map[string]json.RawMessage{"user": user}, nil
}

func apiRegister(c *call) (any, error) {
	origin := c.h.deps.Base()
	email := c.p.str("email")
	password := c.p.str("password")
	body := map[string]any{
		"username":         c.p.str("username"),
		"password":         password,
		"confirm_password": password,
	}
	if strings.TrimSpace(email) != "" {
		body["email"] = email
	}
	raw, err := c.h.edge(c.r.Context(), "POST", "/api/account/registerByPassword", body, "")
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, errors.New("注册返回缺少 token")
	}
	token, ok := rawString(fields["token"])
	if !ok {
		return nil, errors.New("注册返回缺少 token")
	}
	if c.h.deps.Base() != origin {
		return nil, errors.New("注册期间线路已变化，请重新登录")
	}
	session.Set(c.w, c.r, token)
	user := json.RawMessage("null")
	if value, ok := fields["user"]; ok {
		user = value
	}
	return map[string]json.RawMessage{"user": user}, nil
}

func apiLogout(c *call) (any, error) {
	_, _ = c.h.edge(c.r.Context(), "POST", "/api/account/logout", nil, c.h.token(c.r))
	session.Clear(c.w, c.r)
	return map[string]bool{"ok": true}, nil
}

func apiMe(c *call) (any, error) {
	origin := c.h.deps.Base()
	raw, err := c.h.edge(c.r.Context(), "GET", "/api/account/me", nil, c.h.token(c.r))
	if err != nil {
		return nil, err
	}
	if c.h.deps.Base() != origin {
		return nil, errors.New("读取账号期间线路已变化")
	}
	return raw, nil
}