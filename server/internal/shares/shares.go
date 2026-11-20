// Package shares 提供前台分享：创建分享、读取分享信息、校验提取码，
// 以及 /media/share/{code}/{ep} 的 302 播放（复用影片库直链逻辑，不代理字节）。
package shares

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nathanxiangang-web/cilihub/server/internal/filmcatalog"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
	"github.com/nathanxiangang-web/cilihub/server/internal/userauth"
)

const (
	// PathPrefix 是分享播放路由前缀，由 main.go 挂载到 mux。
	PathPrefix = "/media/share/"

	codeLength      = 10
	codeAlphabet    = "0123456789abcdefghijklmnopqrstuvwxyz"
	maxCodeAttempts = 5
	maxPasswordRune = 32
	maxExpiresDays  = 3650
)

// Service 是前台分享服务，依赖数据层、影片库适配器与日志。
type Service struct {
	st  *store.Store
	cat *filmcatalog.Adapter
	log *log.Logger
}

func New(st *store.Store, cat *filmcatalog.Adapter, log *log.Logger) *Service {
	return &Service{st: st, cat: cat, log: log}
}

// Info 是分享落地页所需的最小信息。
type Info struct {
	Title        string
	EpisodeCount int
	NeedPassword bool
	State        string // active|revoked|expired|missing
}

// Create 创建一条作品级分享。episodeNo 由调用方传入但不落库：
// 分享按集取链，播放时由 {ep} 决定文件。
func (s *Service) Create(_ context.Context, user store.UserRow, workID int64, episodeNo int,
	password string, expiresDays int) (store.ShareRow, error) {
	if workID <= 0 {
		return store.ShareRow{}, errors.New("作品不存在")
	}
	if episodeNo < 0 {
		return store.ShareRow{}, errors.New("集数无效")
	}
	if expiresDays < 0 || expiresDays > maxExpiresDays {
		return store.ShareRow{}, errors.New("有效期无效")
	}
	password = strings.TrimSpace(password)
	if utf8.RuneCountInString(password) > maxPasswordRune {
		return store.ShareRow{}, errors.New("提取码最多 32 位")
	}
	passwordHash := ""
	if password != "" {
		hash, err := userauth.HashPassword(password)
		if err != nil {
			return store.ShareRow{}, errors.New("生成提取码失败")
		}
		passwordHash = hash
	}
	now := time.Now().UTC()
	expiresAt := ""
	if expiresDays > 0 {
		expiresAt = now.Add(time.Duration(expiresDays) * 24 * time.Hour).Format(time.RFC3339)
	}

	var lastErr error
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		code, err := newCode()
		if err != nil {
			return store.ShareRow{}, errors.New("生成分享码失败")
		}
		row, err := s.st.CreateShare(store.ShareRow{
			Code:         code,
			WorkID:       workID,
			PasswordHash: passwordHash,
			ExpiresAt:    expiresAt,
			Status:       "active",
			CreatedBy:    user.ID,
		}, now.Format(time.RFC3339))
		if err == nil {
			return row, nil
		}
		lastErr = err
	}
	return store.ShareRow{}, errors.New("创建分享失败：" + lastErr.Error())
}

// Info 读取分享信息：不存在返回 missing，active 时累加访问次数（失败仅记日志）。
func (s *Service) Info(_ context.Context, code string) (Info, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return Info{State: "missing"}, nil
	}
	row, err := s.st.ShareByCode(code)
	if errors.Is(err, store.ErrNotFound) {
		return Info{State: "missing"}, nil
	}
	if err != nil {
		return Info{}, err
	}
	info := Info{
		Title:        row.WorkTitle,
		NeedPassword: row.PasswordHash != "",
		State:        shareState(row, time.Now().UTC()),
	}
	detail, err := s.st.GetWork(row.WorkID)
	switch {
	case err == nil:
		info.Title = detail.Work.Title
		info.EpisodeCount = detail.Work.EpisodeCount
	case errors.Is(err, store.ErrNotFound):
		info.State = "missing"
	default:
		if s.log != nil {
			s.log.Printf("读取分享作品失败 %s: %v", code, err)
		}
	}
	if info.State == "active" {
		if err := s.st.IncShareViews(code); err != nil && s.log != nil {
			s.log.Printf("分享访问计数失败 %s: %v", code, err)
		}
	}
	return info, nil
}

// Unlock 校验分享状态与提取码；无需提取码的分享直接通过。
func (s *Service) Unlock(code, password string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("分享不存在")
	}
	row, err := s.st.ShareByCode(code)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("分享不存在")
	}
	if err != nil {
		return errors.New("读取分享失败")
	}
	switch shareState(row, time.Now().UTC()) {
	case "revoked":
		return errors.New("分享已取消")
	case "expired":
		return errors.New("分享已过期")
	}
	if row.PasswordHash == "" {
		return nil
	}
	if !userauth.VerifyPassword(row.PasswordHash, password) {
		return errors.New("提取码不正确")
	}
	return nil
}

// Handler 返回 /media/share/{code}/{ep} 的播放处理器。
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		deny(w, "仅支持播放请求", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, PathPrefix)
	code, epText, ok := strings.Cut(rest, "/")
	if !ok || code == "" || strings.Contains(epText, "/") {
		deny(w, "分享链接无效", http.StatusNotFound)
		return
	}
	ep, err := strconv.Atoi(epText)
	if err != nil || ep <= 0 {
		deny(w, "分享链接无效", http.StatusNotFound)
		return
	}

	row, err := s.st.ShareByCode(code)
	if errors.Is(err, store.ErrNotFound) {
		deny(w, "分享不存在", http.StatusForbidden)
		return
	}
	if err != nil {
		deny(w, "读取分享失败", http.StatusInternalServerError)
		return
	}
	switch shareState(row, time.Now().UTC()) {
	case "revoked":
		deny(w, "分享已取消", http.StatusForbidden)
		return
	case "expired":
		deny(w, "分享已过期", http.StatusForbidden)
		return
	}
	if row.PasswordHash != "" && !userauth.VerifyPassword(row.PasswordHash, r.URL.Query().Get("pwd")) {
		deny(w, "提取码不正确", http.StatusForbidden)
		return
	}

	detail, err := s.st.GetWork(row.WorkID)
	if errors.Is(err, store.ErrNotFound) {
		deny(w, "影片不存在", http.StatusForbidden)
		return
	}
	if err != nil {
		deny(w, "读取影片信息失败", http.StatusInternalServerError)
		return
	}
	if !detail.Work.Visible {
		deny(w, "影片已下架", http.StatusForbidden)
		return
	}
	var fileID int64
	found := false
	for _, file := range detail.Files {
		if file.EpisodeNo == ep && file.Present {
			fileID = file.FileID
			found = true
			break
		}
	}
	if !found {
		deny(w, "源文件缺失", http.StatusForbidden)
		return
	}
	ref, err := s.st.VisibleFile(fileID)
	if errors.Is(err, store.ErrNotFound) {
		deny(w, "源文件缺失", http.StatusForbidden)
		return
	}
	if err != nil {
		deny(w, "读取影片信息失败", http.StatusInternalServerError)
		return
	}
	if !ref.Visible {
		deny(w, "影片已下架", http.StatusForbidden)
		return
	}
	if !ref.Present {
		deny(w, "源文件缺失", http.StatusForbidden)
		return
	}
	if s.cat == nil {
		deny(w, "视频服务未配置", http.StatusBadGateway)
		return
	}

	// 实时取链与 302 复用影片库现有的 /media/library/{file_id} 播放路径。
	target := r.Clone(r.Context())
	u := *r.URL
	u.Path = filmcatalog.MediaPrefix + strconv.FormatInt(fileID, 10)
	u.RawQuery = ""
	u.Fragment = ""
	target.URL = &u
	s.cat.ServeHTTP(w, target)
}

func deny(w http.ResponseWriter, reason string, status int) {
	http.Error(w, reason, status)
}

func shareState(row store.ShareRow, now time.Time) string {
	if row.Status != "" && row.Status != "active" {
		// 无法识别的状态按已取消处理，避免误放出播放地址。
		return "revoked"
	}
	if row.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, row.ExpiresAt)
		if err != nil || !now.Before(expires) {
			return "expired"
		}
	}
	return "active"
}

func newCode() (string, error) {
	buf := make([]byte, codeLength)
	limit := byte(256 - 256%len(codeAlphabet))
	for i := 0; i < codeLength; {
		if _, err := rand.Read(buf[i : i+1]); err != nil {
			return "", err
		}
		if buf[i] >= limit {
			continue
		}
		buf[i] = codeAlphabet[int(buf[i])%len(codeAlphabet)]
		i++
	}
	return string(buf), nil
}