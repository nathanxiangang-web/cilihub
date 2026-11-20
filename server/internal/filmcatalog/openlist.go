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
	Size int64  `json:"size"`
}


type link struct {
	URL string `json:"raw_url"`
	Dir *bool  `json:"is_dir"`
}

func parseOrigin(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("OPENLIST_ORIGIN 必须是 HTTPS 站点地址")
	}
	return u.Scheme + "://" + u.Host, nil
}

func newOpenListClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// newOpenListWithCreds 用内存凭据构造客户端，不做网络请求；登录推迟到首次 list/link。
func newOpenListWithCreds(origin, username, password string) (*openList, error) {
	canonical, err := parseOrigin(origin)
	if err != nil {
		return nil, err
	}
	if username == "" || password == "" {
		return nil, errors.New("OpenList 账号和密码不能为空")
	}
	return &openList{
		origin: canonical,
		creds:  credentials{Username: username, Password: password},
		client: newOpenListClient(),
	}, nil
}

func newOpenList(origin, credentialFile string) (*openList, error) {
	canonical, err := parseOrigin(origin)
	if err != nil {
		return nil, err
	}
	secret, err := os.ReadFile(credentialFile)
	if err != nil {
		return nil, errors.New("无法读取 OpenList 凭据文件")
	}
	var creds credentials
	if json.Unmarshal(secret, &creds) != nil || creds.Username == "" || creds.Password == "" {
		return nil, errors.New("OpenList 凭据文件需要 username 和 password")
	}
	return &openList{origin: canonical, creds: creds, client: newOpenListClient()}, nil
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
type TestResult struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Entries   int    `json:"entries"`
	Message   string `json:"message"`
}

// TestConnection 登录并列出目标目录，不读写数据库；配置非法时返回错误，连接失败时由结果描述。
func TestConnection(ctx context.Context, cfg ListConfig) (TestResult, error) {
	if err := validateFolder(cfg.Folder); err != nil {
		return TestResult{}, err
	}
	client, err := newOpenListWithCreds(cfg.Origin, cfg.Username, cfg.Password)
	if err != nil {
		return TestResult{}, err
	}
	started := time.Now()
	entries, err := client.list(ctx, cfg.Folder)
	result := TestResult{ElapsedMS: time.Since(started).Milliseconds()}
	if err != nil {
		result.Status, result.Message = testFailure(err)
		return result, nil
	}
	result.OK = true
	result.Status = http.StatusOK
	result.Entries = len(entries)
	result.Message = "连接成功"
	return result, nil
}

func testFailure(err error) (int, string) {
	var status openListStatus
	if !errors.As(err, &status) {
		return http.StatusBadGateway, err.Error()
	}
	switch int(status) {
	case http.StatusUnauthorized, http.StatusForbidden:
		return int(status), "OpenList 账号或密码不正确"
	case http.StatusNotFound:
		return int(status), "映射目录不存在或不是目录"
	default:
		return int(status), status.Error()
	}
}
