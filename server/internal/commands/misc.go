package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/netx"
	"github.com/nathanxiangang-web/cilihub/server/internal/session"
)

const (
	defaultBase  = "https://huangguoai.com"
	publishPage  = "https://huangguoai.pages.dev/"
	updateRepo   = "nathanxiangang-web/cilihub"
	mirrorMax    = 60
	probeTimeout = 8 * time.Second
)

// Version 可在构建期用 -ldflags "-X github.com/nathanxiangang-web/cilihub/server/internal/commands.Version=..." 覆盖。
var Version = "0.1.0"

func miscRoutes() map[string]command {
	return map[string]command{
		"debug_log":           debugLog,
		"net_diag":            netDiag,
		"line_state":          lineState,
		"line_set":            lineSet,
		"line_mirrors":        lineMirrors,
		"line_probe":          lineProbe,
		"check_update":        checkUpdate,
		"open_url":            openURL,
		"set_orientation":     setOrientation,
		"cache_clear":         cacheClear,
		"playback_log_status": playbackLogStatus,
		"playback_log_set":    playbackLogSet,
		"playback_log_write":  playbackLogWrite,
		"playback_log_read":   playbackLogRead,
	}
}

func debugLog(c *call) (any, error) {
	c.h.deps.Log.Print(c.p.str("msg"))
	return nil, nil
}

func netDiag(c *call) (any, error) {
	current := c.h.deps.Base()
	host := ""
	if parsed, err := url.Parse(current); err == nil {
		host = parsed.Hostname()
	}

	dns := map[string]any{"error": "DNS 解析失败"}
	if host != "" {
		dns = dnsSummary(host)
	}

	started := time.Now()
	elapsed := func() int64 { return time.Since(started).Milliseconds() }
	ctx, cancel := context.WithTimeout(c.r.Context(), 20*time.Second)
	defer cancel()
	var httpInfo map[string]any
	resp, err := netx.Fetch(ctx, current+"/", "")
	if err != nil {
		httpInfo = map[string]any{"error": err.Error(), "ms": elapsed()}
	} else {
		_ = resp.Body.Close()
		httpInfo = map[string]any{"status": resp.StatusCode, "ms": elapsed()}
	}

	return map[string]any{
		"base": current,
		"host": host,
		"dns":  dns,
		"http": httpInfo,
		"env": map[string]any{
			"HTTP_PROXY":  proxyEnv("HTTP_PROXY"),
			"HTTPS_PROXY": proxyEnv("HTTPS_PROXY"),
			"ALL_PROXY":   proxyEnv("ALL_PROXY"),
			"NO_PROXY":    noProxyEnv(),
			"CILIHUB_PROXY": proxyEnv("CILIHUB_PROXY"),
		},
	}, nil
}

func dnsSummary(host string) map[string]any {
	addrs, err := net.LookupHost(host)
	if err != nil {
		return map[string]any{"error": "DNS 解析失败"}
	}
	sort.Strings(addrs)
	unique := make([]string, 0, len(addrs))
	hasFakeIP := false
	for _, addr := range addrs {
		if len(unique) > 0 && unique[len(unique)-1] == addr {
			continue
		}
		unique = append(unique, addr)
		if ip := net.ParseIP(addr); ip != nil {
			if v4 := ip.To4(); v4 != nil && v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
				hasFakeIP = true
			}
		}
	}
	return map[string]any{"count": len(unique), "has_fake_ip": hasFakeIP}
}

func proxyEnv(name string) any {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	return netx.RedactProxy(value)
}

func noProxyEnv() any {
	if strings.TrimSpace(os.Getenv("NO_PROXY")) == "" {
		return nil
	}
	return "[已配置，列表已隐藏]"
}

func lineState(c *call) (any, error) {
	fixed := []map[string]string{
		{"name": "固定线路 1", "url": "https://huangguoai.com"},
		{"name": "固定线路 2", "url": "https://huangguoai.ai"},
	}
	return map[string]any{"base": c.h.deps.Base(), "default": defaultBase, "fixed": fixed}, nil
}

func lineSet(c *call) (any, error) {
	previous := c.h.deps.Base()
	next, err := c.h.deps.SetBase(c.p.str("url"))
	if err != nil {
		return nil, err
	}
	if previous != next {
		session.Clear(c.w, c.r)
	}
	return next, nil
}

func lineMirrors(c *call) (any, error) {
	ctx, cancel := context.WithTimeout(c.r.Context(), 20*time.Second)
	defer cancel()

	html, err := fetchText(ctx, publishPage)
	if err != nil {
		return nil, err
	}
	script := scriptSrc(html)
	if script == "" {
		script = "publish.js"
	}
	scriptURL := absoluteURL(publishPage, script)
	js, err := fetchText(ctx, scriptURL)
	if err != nil {
		return nil, err
	}

	domains := jsArray(js, "var urls")
	subs := jsArray(js, "var subdomains")
	if len(domains) == 0 {
		return nil, errors.New("地址发布页格式有变化：没找到镜像域名")
	}

	items := make([]map[string]string, 0, mirrorMax)
	seen := make(map[string]bool, mirrorMax)
	for _, domain := range domains {
		domain = strings.TrimRight(domain, "/")
		if domain == "" {
			continue
		}
		hosts := []string{domain}
		if len(subs) > 0 {
			hosts = make([]string, 0, len(subs))
			for _, sub := range subs {
				hosts = append(hosts, sub+"."+domain)
			}
		}
		for _, host := range hosts {
			if seen[host] {
				continue
			}
			seen[host] = true
			items = append(items, map[string]string{"url": "https://" + host, "host": host})
			if len(items) >= mirrorMax {
				return map[string]any{"source": publishPage, "script": scriptURL, "items": items}, nil
			}
		}
	}
	if len(items) == 0 {
		return nil, errors.New("地址发布页里没有可用的镜像地址")
	}
	return map[string]any{"source": publishPage, "script": scriptURL, "items": items}, nil
}

func lineProbe(c *call) (any, error) {
	urls := c.p.strs("urls")
	ctx := c.r.Context()
	out := make([]any, 0, len(urls))
	for start := 0; start < len(urls); start += 6 {
		end := start + 6
		if end > len(urls) {
			end = len(urls)
		}
		chunk := urls[start:end]
		results := make([]any, len(chunk))
		var wg sync.WaitGroup
		for i, target := range chunk {
			wg.Add(1)
			go func(i int, target string) {
				defer wg.Done()
				results[i] = probeOne(ctx, target)
			}(i, target)
		}
		wg.Wait()
		out = append(out, results...)
	}
	return out, nil
}

func probeOne(parent context.Context, target string) map[string]any {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()
	started := time.Now()
	resp, err := netx.Fetch(ctx, strings.TrimRight(target, "/")+"/", "")
	ms := time.Since(started).Milliseconds()
	if err != nil {
		return map[string]any{"url": target, "ok": false, "ms": ms, "error": err.Error()}
	}
	_ = resp.Body.Close()
	return map[string]any{"url": target, "ok": resp.StatusCode < 400, "status": resp.StatusCode, "ms": ms}
}

func checkUpdate(c *call) (any, error) {
	current := Version
	releaseURL := fmt.Sprintf("https://github.com/%s/releases", updateRepo)
	ctx, cancel := context.WithTimeout(c.r.Context(), 12*time.Second)
	defer cancel()
	resp, err := netx.Fetch(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updateRepo), "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return map[string]any{
			"current": current, "latest": "", "has_update": false, "url": releaseURL,
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub 返回 HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var release struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Body        string `json:"body"`
		Name        string `json:"name"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, err
	}
	link := release.HTMLURL
	if link == "" {
		link = releaseURL
	}
	return map[string]any{
		"current":      current,
		"latest":       strings.TrimLeft(release.TagName, "vV"),
		"tag":          release.TagName,
		"has_update":   release.TagName != "" && isNewer(release.TagName, current),
		"url":          link,
		"name":         release.Name,
		"published_at": release.PublishedAt,
		"notes":        truncateRunes(release.Body, 800),
	}, nil
}

func openURL(c *call) (any, error) {
	if _, err := netx.CheckTarget(c.p.str("url")); err != nil {
		return nil, err
	}
	return nil, nil
}

func setOrientation(c *call) (any, error) {
	return nil, nil
}

func cacheClear(c *call) (any, error) {
	c.h.deps.Media.Clear()
	return nil, nil
}

func fetchText(ctx context.Context, target string) (string, error) {
	resp, err := netx.Fetch(ctx, target, "")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func scriptSrc(html string) string {
	for {
		at := strings.Index(html, `src="`)
		if at < 0 {
			return ""
		}
		rest := html[at+5:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return ""
		}
		candidate := rest[:end]
		if strings.Contains(candidate, ".js") {
			return candidate
		}
		html = rest[end:]
	}
}

func absoluteURL(base, rel string) string {
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return rel
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if strings.HasPrefix(rel, "/") {
		return origin + rel
	}
	return origin + "/" + rel
}

func jsArray(src, decl string) []string {
	at := strings.Index(src, decl)
	if at < 0 {
		return nil
	}
	rest := src[at:]
	open := strings.IndexByte(rest, '[')
	if open < 0 {
		return nil
	}
	closeAt := strings.IndexByte(rest[open:], ']')
	if closeAt < 0 {
		return nil
	}
	body := stripLineComments(rest[open+1 : open+closeAt])
	out := make([]string, 0)
	for _, raw := range strings.Split(body, ",") {
		value := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), `'"`))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// stripLineComments 去掉 // 行尾注释，但保留引号内的 URL。
func stripLineComments(body string) string {
	var out strings.Builder
	var quote byte
	inQuote := false
	for i := 0; i < len(body); i++ {
		ch := body[i]
		if inQuote {
			out.WriteByte(ch)
			if ch == quote {
				inQuote = false
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			inQuote = true
			quote = ch
			out.WriteByte(ch)
			continue
		}
		if ch == '/' && i+1 < len(body) && body[i+1] == '/' {
			for i < len(body) && body[i] != '\n' {
				i++
			}
			if i < len(body) {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func verParts(v string) []uint64 {
	v = strings.TrimLeft(strings.TrimSpace(v), "vV")
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	out := make([]uint64, len(parts))
	for i, part := range parts {
		if n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64); err == nil {
			out[i] = n
		}
	}
	return out
}

func isNewer(latest, current string) bool {
	a, b := verParts(latest), verParts(current)
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y uint64
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

/* ───────────────────────── 播放诊断日志 ───────────────────────── */

const (
	pbMaxFile   = 2 * 1024 * 1024
	pbMaxBatch  = 128
	pbKeepLines = 512
)

var (
	pbSeq     atomic.Uint64
	pbMu      sync.Mutex
	pbEnabled bool
	pbLines   []string
	pbSize    int

)

func playbackLogStatus(c *call) (any, error) {
	return playbackStatus(), nil
}

func playbackLogSet(c *call) (any, error) {
	enabled := c.p.flag("enabled")
	pbMu.Lock()
	pbEnabled = enabled

	pbMu.Unlock()
	pbRecord(c, "diagnostics.toggle", map[string]any{
		"enabled": enabled,
		"version": Version,
		"os":      runtime.GOOS,
	})
	return playbackStatus(), nil
}

func playbackLogWrite(c *call) (any, error) {
	if !playbackEnabled() {
		return nil, nil
	}
	value, ok := c.p.pick("entries")
	if !ok {
		return nil, nil
	}
	entries, ok := value.([]any)
	if !ok {
		return nil, errors.New("日志条目无效")
	}
	if len(entries) > pbMaxBatch {
		return nil, errors.New("日志批次过大")
	}
	lines := make([]string, 0, len(entries))
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("日志条目无效")
		}
		event, _ := entry["event"].(string)
		if !validLogEvent(event) {
			return nil, errors.New("日志条目无效")
		}
		encoded, err := json.Marshal(entry["data"])
		if err != nil || len(encoded) > 8192 {
			return nil, errors.New("日志条目无效")
		}
		lines = append(lines, buildLogLine("web."+event, entry["data"]))
	}
	for _, line := range lines {
		c.h.deps.Log.Print(line)
	}
	pbStore(lines)
	return nil, nil
}

func playbackLogRead(c *call) (any, error) {
	pbMu.Lock()
	defer pbMu.Unlock()
	if pbSize > pbMaxFile+16384 {
		return nil, errors.New("日志文件超出大小限制")
	}
	text := strings.Join(pbLines, "\n")
	if text != "" {
		text += "\n"
	}
	return text, nil
}

func playbackStatus() map[string]any {
	pbMu.Lock()
	defer pbMu.Unlock()
	return map[string]any{"enabled": pbEnabled, "path": nil, "error": nil}
}

func playbackEnabled() bool {
	pbMu.Lock()
	defer pbMu.Unlock()
	return pbEnabled
}

func pbRecord(c *call, event string, data any) {
	line := buildLogLine(event, data)
	c.h.deps.Log.Print(line)
	pbStore([]string{line})
}

func pbStore(lines []string) {
	pbMu.Lock()
	defer pbMu.Unlock()
	for _, line := range lines {
		pbLines = append(pbLines, line)
		pbSize += len(line) + 1
	}
	for len(pbLines) > pbKeepLines || (len(pbLines) > 1 && pbSize > pbMaxFile) {
		pbSize -= len(pbLines[0]) + 1
		pbLines = pbLines[1:]
	}
}

func buildLogLine(event string, data any) string {
	entry := map[string]any{
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"pid":    os.Getpid(),
		"seq":    pbSeq.Add(1) - 1,
		"event":  event,
		"strict": true,
		"data":   scrubData(data, 0),
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func validLogEvent(event string) bool {
	if event == "" || len(event) > 80 {
		return false
	}
	for i := 0; i < len(event); i++ {
		ch := event[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func scrubData(v any, depth int) any {
	if depth > 5 {
		return nil
	}
	switch x := v.(type) {
	case string:
		if strings.Contains(x, "://") || strings.Contains(strings.ToLower(x), "%3a") {
			return "[redacted]"
		}
		return truncateRunes(x, 160)
	case []any:
		limit := len(x)
		if limit > 24 {
			limit = 24
		}
		out := make([]any, 0, limit)
		for _, item := range x[:limit] {
			out = append(out, scrubData(item, depth+1))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		count := 0
		for key, value := range x {
			if count >= 64 {
				break
			}
			if sensitiveLogKey(key) {
				continue
			}
			out[truncateRunes(key, 64)] = scrubData(value, depth+1)
			count++
		}
		return out
	default:
		return v
	}
}

func sensitiveLogKey(key string) bool {
	lowered := strings.ToLower(key)
	for _, blocked := range []string{"url", "src", "token", "password", "cookie", "authorization", "body", "message", "reason"} {
		if strings.Contains(lowered, blocked) {
			return true
		}
	}
	return false
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}