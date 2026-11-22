package cloudapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const magJobs = "/api/v1/jobs"
const searchLifetime = 15 * time.Minute

var magJobID = regexp.MustCompile(`^j[0-9]{1,20}$`)

type magSearch struct {
	origin, key string
	client      *http.Client
	mu          sync.Mutex
	jobs        map[string]searchJob
}

type searchJob struct {
	owner    int64
	upstream string
	until    time.Time
}

type magResponse struct {
	Job   string      `json:"job_id"`
	State string      `json:"state"`
	Items []magResult `json:"items"`
}

type magResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Filename string `json:"filename"`
	Magnet   string `json:"magnet"`
	Size     string `json:"size"`
	ListSize string `json:"list_size,omitempty"`
}

func newMagSearch(origin, keyFile string) (*magSearch, error) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return nil, nil
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("磁力搜索服务须使用 HTTP(S) 源地址")
	}
	raw, err := os.ReadFile(keyFile)
	key := strings.TrimSpace(string(raw))
	if err != nil || len(key) < 32 || strings.ContainsFunc(key, unicode.IsSpace) {
		return nil, errors.New("磁力搜索服务的接口令牌无效")
	}
	return &magSearch{origin: strings.TrimRight(origin, "/"), key: key, jobs: make(map[string]searchJob),
		client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *Service) startSearch(w http.ResponseWriter, r *http.Request, owner int64) {
	if s.magnets == nil {
		fail(w, 503, "磁力搜索暂未配置")
		return
	}
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		fail(w, 415, "请求须使用 JSON")
		return
	}
	var input struct {
		Query string `json:"query"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		fail(w, 400, "搜索参数无效")
		return
	}
	query := strings.TrimSpace(input.Query)
	if query == "" || utf8.RuneCountInString(query) > 80 || strings.ContainsFunc(query, unicode.IsControl) {
		fail(w, 400, "请输入 1 到 80 个字符的关键词")
		return
	}
	body, _ := json.Marshal(map[string]any{"keywords": []string{query}, "limit": 5, "detail": true})
	job, status, err := s.magnets.call(r.Context(), http.MethodPost, magJobs, bytes.NewReader(body))
	if err != nil || status != http.StatusAccepted || !magJobID.MatchString(job.Job) {
		searchFailure(w, status)
		return
	}
	key := rand.Text()
	now := time.Now()
	m := s.magnets
	m.mu.Lock()
	for id, v := range m.jobs {
		if !now.Before(v.until) {
			delete(m.jobs, id)
		}
	}
	m.jobs[key] = searchJob{owner: owner, upstream: job.Job, until: now.Add(searchLifetime)}
	m.mu.Unlock()
	write(w, http.StatusAccepted, map[string]string{"key": key, "state": "running"})
}

func (s *Service) searchStatus(w http.ResponseWriter, r *http.Request, owner int64) {
	m := s.magnets
	if m == nil {
		fail(w, 503, "磁力搜索暂未配置")
		return
	}
	m.mu.Lock()
	job, exists := m.jobs[r.PathValue("key")]
	m.mu.Unlock()
	if !exists || job.owner != owner || !time.Now().Before(job.until) {
		fail(w, 404, "搜索已过期，请重新搜索")
		return
	}
	out, status, err := m.call(r.Context(), http.MethodGet, magJobs+"/"+job.upstream+"/results", nil)
	if err != nil || status != http.StatusOK || out.Job != job.upstream {
		searchFailure(w, status)
		return
	}
	items := make([]magResult, 0, len(out.Items))
	seen := make(map[string]bool)
	for _, item := range out.Items {
		magnet, id, err := normalizeMagnet(item.Magnet)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		item.ID = id
		item.Magnet = magnet
		item.Title = validResultTitle(item.Title)
		item.Filename = validResultTitle(item.Filename)
		if item.Title == "" {
			item.Title = item.Filename
		}
		if item.Title == "" {
			item.Title = "磁力资源 " + id[:8]
		}
		if item.Size == "" {
			item.Size = item.ListSize
		}
		item.ListSize = ""
		items = append(items, item)
	}
	write(w, 200, map[string]any{"state": out.State, "items": items})
}

func validResultTitle(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1024 || strings.ContainsRune(value, 0) {
		return ""
	}
	return value
}

func searchFailure(w http.ResponseWriter, status int) {
	switch status {
	case 404:
		fail(w, 404, "搜索已过期，请重新搜索")
	case 409:
		fail(w, 409, "已有磁力搜索正在运行，请稍后再试")
	case 429:
		fail(w, 429, "搜索请求过多，请稍后再试")
	default:
		fail(w, 502, "磁力搜索服务暂时不可用")
	}
}

func (s *magSearch) call(ctx context.Context, method, path string, body io.Reader) (magResponse, int, error) {
	var out magResponse
	req, err := http.NewRequestWithContext(ctx, method, s.origin+path, body)
	if err != nil {
		return out, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return out, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return out, resp.StatusCode, nil
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&out)
	return out, resp.StatusCode, err
}
