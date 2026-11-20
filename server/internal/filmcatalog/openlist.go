package filmcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type openList struct {
	origin string
	creds  credentials
	client *http.Client
	mu     sync.Mutex
	token  string
}

type entry struct {
	Name string `json:"name"`
	Dir  bool   `json:"is_dir"`
}

type link struct {
	URL string `json:"raw_url"`
	Dir *bool  `json:"is_dir"`
}

func newOpenList(origin, credentialFile string) (*openList, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("OPENLIST_ORIGIN 必须是 HTTPS 站点地址")
	}
	secret, err := os.ReadFile(credentialFile)
	if err != nil {
		return nil, errors.New("无法读取 OpenList 凭据文件")
	}
	var creds credentials
	if json.Unmarshal(secret, &creds) != nil || creds.Username == "" || creds.Password == "" {
		return nil, errors.New("OpenList 凭据文件需要 username 和 password")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &openList{
		origin: u.Scheme + "://" + u.Host,
		creds:  creds,
		client: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type openListStatus int

func (s openListStatus) Error() string { return fmt.Sprintf("OpenList 状态 %d", s) }

func (o *openList) post(ctx context.Context, endpoint string, input, output any, token, userAgent string) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.origin+endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("OpenList %s 请求失败", endpoint)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return openListStatus(resp.StatusCode)
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope); err != nil {
		return errors.New("OpenList 响应格式无效")
	}
	if envelope.Code != http.StatusOK {
		return openListStatus(envelope.Code)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, output) != nil {
		return errors.New("OpenList 数据格式无效")
	}
	return nil
}

func (o *openList) session(ctx context.Context, expired string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.token != "" && o.token != expired {
		return o.token, nil
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := o.post(ctx, "/api/auth/login", o.creds, &result, "", ""); err != nil {
		return "", err
	}
	if result.Token == "" {
		return "", errors.New("OpenList 登录响应缺少令牌")
	}
	o.token = result.Token
	return o.token, nil
}

func (o *openList) read(ctx context.Context, endpoint string, input, output any, userAgent string) error {
	token, err := o.session(ctx, "")
	if err != nil {
		return err
	}
	err = o.post(ctx, endpoint, input, output, token, userAgent)
	if !errors.Is(err, openListStatus(http.StatusUnauthorized)) {
		return err
	}
	token, err = o.session(ctx, token)
	if err != nil {
		return err
	}
	return o.post(ctx, endpoint, input, output, token, userAgent)
}

func (o *openList) list(ctx context.Context, path string) ([]entry, error) {
	const pageSize = 200
	var entries []entry
	for page := 1; ; page++ {
		input := map[string]any{"path": path, "page": page, "per_page": pageSize}
		var result struct {
			Content []entry `json:"content"`
			Total   int     `json:"total"`
		}
		if err := o.read(ctx, "/api/fs/list", input, &result, ""); err != nil {
			return nil, err
		}
		entries = append(entries, result.Content...)
		if len(entries) >= result.Total || len(result.Content) == 0 {
			return entries, nil
		}
	}
}

func (o *openList) link(ctx context.Context, path, userAgent string) (link, error) {
	var item link
	err := o.read(ctx, "/api/fs/get", map[string]string{"path": path}, &item, userAgent)
	return item, err
}
