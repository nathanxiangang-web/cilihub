// Package adminauth 提供后台管理员的密码哈希、会话 token 与请求身份解析。
// 它与前台用户体系相互独立，唯一的存储入口是 store.Store。
package adminauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

const (
	CookieName = "cili_admin"
	SessionTTL = 30 * 24 * time.Hour

	pbkdf2Iterations = 600000
	pbkdf2KeyLength  = 32
	pbkdf2SaltLength = 16
	tokenLength      = 32
)

// ErrUnauthorized 表示请求没有有效会话，调用方映射为 401。
var ErrUnauthorized = errors.New("请先登录")

// HashPassword 生成 pbkdf2$sha256$600000$<saltB64>$<hashB64> 格式的密码哈希。
func HashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成密码盐失败: %w", err)
	}
	hash := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyLength)
	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s",
		pbkdf2Iterations,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword 校验明文密码是否与编码后的哈希匹配。
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[2])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken 生成会话 token 及其存储用的哈希；token 用于 cookie，tokenHash 用于入库。
func NewToken() (string, string) {
	buf := make([]byte, tokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", ""
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token)
}

// HashToken 返回 token 的 sha256 十六进制摘要。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

// SetCookie 写入管理员会话 cookie。
func SetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(SessionTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
}

// ClearCookie 使管理员会话 cookie 失效。
func ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
}

// AdminFrom 解析请求 cookie 对应的管理员；任何失败都返回 ErrUnauthorized。
func AdminFrom(r *http.Request, st *store.Store) (store.AdminUser, error) {
	if st == nil {
		return store.AdminUser{}, ErrUnauthorized
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return store.AdminUser{}, ErrUnauthorized
	}
	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return store.AdminUser{}, ErrUnauthorized
	}
	admin, err := st.SessionAdmin(HashToken(token), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return store.AdminUser{}, ErrUnauthorized
	}
	return admin, nil
}

// TokenHashFrom 返回请求 cookie 的哈希，无会话时返回 ""；供改密时保留当前会话使用。
func TokenHashFrom(r *http.Request) string {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return ""
	}
	return HashToken(token)
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// pbkdf2SHA256 是 RFC 8018 PBKDF2 的 HMAC-SHA256 实现。
func pbkdf2SHA256(password, salt []byte, iterations, keyLength int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLength + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)

	var counter [4]byte
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		prf.Write(counter[:])
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iterations; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLength]
}