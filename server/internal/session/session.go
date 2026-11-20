// Package session 保存上游 token 的服务端 HttpOnly cookie。
package session

import (
	"net/http"
	"strings"
)

const (
	CookieName = "hg_token"
	maxAge     = 2592000
)

// Token 返回请求携带的上游 token；无 cookie 或为空时返回 ""。
func Token(r *http.Request) string {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

// Set 覆盖写入会话 cookie。
func Set(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure(r),
	})
}

// Clear 删除会话 cookie。
func Clear(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure(r),
	})
}

func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}