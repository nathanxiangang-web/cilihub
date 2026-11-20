package commands

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
	"github.com/nathanxiangang-web/cilihub/server/internal/userauth"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

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

func userPayload(row store.UserRow) map[string]any {
	return map[string]any{"user": map[string]any{
		"id":       row.ID,
		"username": row.Username,
		"nickname": row.Username,
		"avatar":   "",
	}}
}

func appBoot(c *call) (any, error) {
	row, loggedIn, err := c.h.currentUser(c.r)
	if err != nil {
		return nil, errors.New("读取账号失败：" + err.Error())
	}
	user := any(nil)
	if loggedIn {
		user = map[string]any{
			"id":       row.ID,
			"username": row.Username,
			"nickname": row.Username,
			"avatar":   "",
		}
	}
	return map[string]any{
		"user":            user,
		"logged_in":       loggedIn,
		"base":            c.h.deps.Base(),
		"strict_security": true,
		"cli_relaxed":     false,
	}, nil
}

// securitySet：服务端固定严格模式，浏览器端无法放宽。
func securitySet(c *call) (any, error) {
	return true, nil
}

func apiRegister(c *call) (any, error) {
	username := strings.TrimSpace(c.p.str("username"))
	password := c.p.str("password")
	if !usernamePattern.MatchString(username) {
		return nil, errors.New("账号需 3-32 位字母、数字、下划线或短横线")
	}
	if utf8.RuneCountInString(password) < 6 {
		return nil, errors.New("密码至少 6 位")
	}
	hash, err := userauth.HashPassword(password)
	if err != nil {
		return nil, errors.New("注册失败：" + err.Error())
	}
	now := time.Now().UTC().Format(time.RFC3339)
	row, err := c.h.deps.Store.CreateUser(username, hash, "", now)
	if err != nil {
		if err.Error() == "该账号已被注册" {
			return nil, err
		}
		return nil, errors.New("注册失败：" + err.Error())
	}
	if err := c.h.startUserSession(c.w, c.r, row.ID); err != nil {
		return nil, err
	}
	return userPayload(row), nil
}

func apiLogin(c *call) (any, error) {
	username := strings.TrimSpace(c.p.str("username"))
	password := c.p.str("password")
	if username == "" || password == "" {
		return nil, errors.New("账号或密码不正确")
	}
	row, hash, err := c.h.deps.Store.FindUserCredential(username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New("账号或密码不正确")
		}
		return nil, errors.New("登录失败：" + err.Error())
	}
	if !userauth.VerifyPassword(hash, password) {
		return nil, errors.New("账号或密码不正确")
	}
	if row.Status != "active" {
		return nil, errors.New("账号已被禁用")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	token, tokenHash := userauth.NewToken()
	expiresAt := time.Now().UTC().Add(userauth.SessionTTL).Format(time.RFC3339)
	if err := c.h.deps.Store.PutUserSession(tokenHash, row.ID, expiresAt, now); err != nil {
		return nil, errors.New("创建会话失败：" + err.Error())
	}
	userauth.SetCookie(c.w, c.r, token)
	if err := c.h.deps.Store.TouchUserLogin(row.ID, now); err != nil {
		c.h.deps.Log.Printf("更新用户 %d 最近登录时间失败: %v", row.ID, err)
	}
	return userPayload(row), nil
}

func apiLogout(c *call) (any, error) {
	if token := userauth.TokenFrom(c.r); token != "" {
		if err := c.h.deps.Store.DeleteUserSession(userauth.HashToken(token)); err != nil {
			c.h.deps.Log.Printf("删除用户会话失败: %v", err)
		}
	}
	userauth.ClearCookie(c.w, c.r)
	return map[string]bool{"ok": true}, nil
}

func apiMe(c *call) (any, error) {
	row, loggedIn, err := c.h.currentUser(c.r)
	if err != nil {
		return nil, errors.New("读取账号失败：" + err.Error())
	}
	if !loggedIn {
		return map[string]any{"user": nil}, nil
	}
	return userPayload(row), nil
}

// currentUser 通过 cili_user 会话读出当前用户；无会话或会话失效时返回 loggedIn=false。
func (h *router) currentUser(r *http.Request) (store.UserRow, bool, error) {
	token := userauth.TokenFrom(r)
	if token == "" {
		return store.UserRow{}, false, nil
	}
	row, err := h.deps.Store.UserBySession(userauth.HashToken(token), time.Now().UTC().Format(time.RFC3339))
	if errors.Is(err, store.ErrNotFound) {
		return store.UserRow{}, false, nil
	}
	if err != nil {
		return store.UserRow{}, false, err
	}
	return row, true, nil
}

func (h *router) startUserSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, tokenHash := userauth.NewToken()
	now := time.Now().UTC().Format(time.RFC3339)
	expiresAt := time.Now().UTC().Add(userauth.SessionTTL).Format(time.RFC3339)
	if err := h.deps.Store.PutUserSession(tokenHash, userID, expiresAt, now); err != nil {
		return errors.New("创建会话失败：" + err.Error())
	}
	userauth.SetCookie(w, r, token)
	return nil
}