package netx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"strings"
	"time"
)

const (
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	maxRedirects   = 5
	requestTimeout = 20 * time.Second
	dialTimeout    = 10 * time.Second
	keepAlive      = 60 * time.Second
	retryDelay     = 350 * time.Millisecond
)

var transport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           safeDialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          32,
	MaxIdleConnsPerHost:   8,
	IdleConnTimeout:       keepAlive,
	TLSHandshakeTimeout:   dialTimeout,
	ResponseHeaderTimeout: requestTimeout,
}

var redirectClient = &http.Client{
	Transport: transport,
	Timeout:   requestTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("重定向次数超过限制")
		}
		next, err := CheckTarget(req.URL.String())
		if err != nil {
			return err
		}
		if prev := via[len(via)-1].URL; prev.Scheme == "https" && next.Scheme != "https" {
			return errors.New("禁止 HTTPS 降级重定向")
		}
		return nil
	},
}

var rawClient = &http.Client{
	Transport: transport,

	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type businessError struct{ msg string }

func (e *businessError) Error() string { return e.msg }

type httpError struct {
	status int
	text   string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.text) }

// Edge 以边缘信封调用上游业务接口，并解开 {status,data,msg} 包装。
func Edge(ctx context.Context, origin, method, path string, body []byte, token string) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(origin), "/")
	endpoint := base + "/api/edge/request"
	if _, err := CheckTarget(endpoint); err != nil {
		return nil, err
	}
	envelope := map[string]string{
		"method":       method,
		"path":         path,
		"body":         "",
		"content_type": "",
	}
	if body != nil {
		envelope["body"] = base64.StdEncoding.EncodeToString(body)
		envelope["content_type"] = "application/json"
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("请求参数无效")
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		data, err := postEnvelope(ctx, endpoint, base, payload, token)
		if err == nil {
			return data, nil
		}
		lastErr = err
		var business *businessError
		if errors.As(err, &business) || !retryableError(err) || attempt == 1 {
			break
		}
		time.Sleep(retryDelay * time.Duration(attempt+1))
	}
	return nil, lastErr
}

func postEnvelope(ctx context.Context, endpoint, base string, payload []byte, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("目标地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", base+"/")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := redirectClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("401")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpError{status: resp.StatusCode, text: truncate(text, 160)}
	}
	if len(bytes.TrimSpace(text)) == 0 {
		return []byte("null"), nil
	}
	trimmed := bytes.TrimSpace(text)
	if bytes.Equal(trimmed, []byte("null")) {
		return []byte("null"), nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(text, &value); err != nil {
		return nil, fmt.Errorf("%v: %s", err, truncate(text, 160))
	}
	var status int64
	if raw, ok := value["status"]; ok && json.Unmarshal(raw, &status) == nil && status == 1 {
		data, ok := value["data"]
		if !ok || len(bytes.TrimSpace(data)) == 0 {
			return []byte("null"), nil
		}
		return data, nil
	}
	msg := ""
	if raw, ok := value["msg"]; ok {
		_ = json.Unmarshal(raw, &msg)
	}
	if msg == "" {
		msg = "接口返回失败"
	}
	return nil, &businessError{msg: msg}
}

func retryableError(err error) bool {
	var business *businessError
	if errors.As(err, &business) {
		return false
	}
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr.status >= 500 && httpErr.status <= 599
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tunnel") || strings.Contains(msg, "proxy")
}

// Fetch 拉取不可信目标的原始字节；手动跟随最多 5 跳，每跳重新校验目标。
func Fetch(ctx context.Context, rawURL, rangeHeader string) (*http.Response, error) {
	current, err := CheckTarget(rawURL)
	if err != nil {
		return nil, err
	}
	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, errors.New("目标地址无效")
		}
		req.Header.Set("User-Agent", userAgent)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		resp, err := rawClient.Do(req)
		if err != nil {
			return nil, err
		}
		if !isRedirect(resp.StatusCode) {
			return resp, nil
		}
		if hop >= maxRedirects {
			resp.Body.Close()
			return nil, errors.New("重定向次数超过限制")
		}
		location := resp.Header.Get("Location")
		resp.Body.Close()
		if location == "" {
			return nil, errors.New("重定向缺少有效 Location")
		}
		next, err := current.Parse(location)
		if err != nil {
			return nil, errors.New("重定向地址无效")
		}
		next, err = CheckTarget(next.String())
		if err != nil {
			return nil, err
		}
		if current.Scheme == "https" && next.Scheme != "https" {
			return nil, errors.New("禁止 HTTPS 降级重定向")
		}
		current = next
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func truncate(text []byte, limit int) string {
	runes := []rune(string(text))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

// RedactProxy 只保留代理的协议、主机与端口，丢弃认证信息与查询参数。
func RedactProxy(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Scheme == "" {
		return "[已配置，原始值已隐藏]"
	}
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	safe := u.Scheme + "://" + host
	if port := u.Port(); port != "" {
		safe += ":" + port
	}
	return safe
}
