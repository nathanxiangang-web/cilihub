// Package media 把桌面端 hg:// 的封面与 HLS 代理整体搬到同源 /media。
//
// 浏览器不能直接播放跨域 HLS（key 与分片会被 CORS 拦住），封面又是 AES-CBC 密文，
// 所以图片、播放列表与分片统一由服务端取回并改写后返回。
package media

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/nathanxiangang-web/cilihub/server/internal/netx"
)

// Prefix 是媒体代理的挂载前缀，前端 src/api.ts 的 LOCAL 在 Web 下取同值。
const Prefix = "/media"

// 单次上游响应体的读取上限，避免异常响应把进程内存打满。
const maxBodyBytes = 64 << 20

var (
	imgKey = []byte("f5d965df75336270")
	imgIV  = []byte("97b60394abc2fbe1")
)

// Service 持有封面 / 播放列表 / 分片的内存缓存。
type Service struct {
	img *byteCache
	hls *byteCache
	seg *byteCache

	// directSeg 为真时播放字节不经过本服务：分片清单改写成上游绝对地址，
	// /seg 对分片直接 302。由 CILIHUB_MEDIA_MODE / CILIHUB_SEG_PROXY 决定。
	directSeg bool
	// resolveRedirect 为真时 302 前先跟随上游重定向取最终地址。
	resolveRedirect bool
}

// New 构造媒体服务；缓存上限与 TTL 见 design.md §2.2 C-MEDIA@1。
func New() *Service {
	proxyMode := strings.EqualFold(strings.TrimSpace(os.Getenv("CILIHUB_MEDIA_MODE")), "proxy")
	return &Service{
		img:             newByteCache(cacheConfig{maxEntries: 256, maxBytes: 128 << 20, maxEntryBytes: 128 << 20, ttl: imgTTL}),
		hls:             newByteCache(cacheConfig{maxEntries: 128, ttl: playlistTTL}),
		seg:             newByteCache(cacheConfig{maxEntries: 256, maxBytes: 128 << 20, maxEntryBytes: 4 << 20, ttl: segmentTTL}),
		directSeg:       !proxyMode && !envBool("CILIHUB_SEG_PROXY"),
		resolveRedirect: envBool("CILIHUB_RESOLVE_REDIRECT"),
	}
}

// Clear 清空全部内存缓存（cache_clear 命令使用）。
func (s *Service) Clear() {
	s.img.clear()
	s.hls.clear()
	s.seg.clear()
}

// Handler 返回挂载在 /media/ 下的 HTTP 处理器。
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeText(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	route := strings.TrimPrefix(r.URL.Path, Prefix)
	target := queryParam(r.URL.RawQuery, "u")
	if target == "" {
		writeText(w, http.StatusBadRequest, "missing u")
		return
	}
	if _, err := netx.CheckTarget(target); err != nil {
		writeText(w, http.StatusForbidden, err.Error())
		return
	}
	ctx := r.Context()
	switch route {
	case "/img":
		s.serveImage(ctx, w, target)
	case "/hls":
		s.servePlaylist(ctx, w, target)
	case "/seg":
		s.serveSegment(ctx, w, r.Header.Get("Range"), target, queryParam(r.URL.RawQuery, "p") == "1")
	default:
		writeText(w, http.StatusNotFound, "not found")
	}
}

func (s *Service) serveImage(ctx context.Context, w http.ResponseWriter, target string) {
	if ctype, data, ok := s.img.get(target); ok {
		writeBytes(w, http.StatusOK, ctype, data, "public, max-age=86400")
		return
	}
	body, err := fetchBody(ctx, target, "")
	if err != nil {
		writeText(w, http.StatusBadGateway, err.Error())
		return
	}
	plain := body
	if imageKind(plain) == "" {
		dec, ok := aesCBCDecrypt(plain)
		if !ok {
			writeText(w, http.StatusBadGateway, "封面解密失败")
			return
		}
		plain = stripPadding(dec)
	}
	ctype := imageKind(plain)
	if ctype == "" {
		ctype = "image/jpeg"
	}
	s.img.put(target, ctype, plain)
	writeBytes(w, http.StatusOK, ctype, plain, "public, max-age=86400")
}

func (s *Service) servePlaylist(ctx context.Context, w http.ResponseWriter, target string) {
	if _, data, ok := s.hls.get(target); ok {
		writeBytes(w, http.StatusOK, playlistContentType, data, "no-store")
		return
	}
	body, err := fetchBody(ctx, target, "")
	if err != nil {
		writeText(w, http.StatusBadGateway, err.Error())
		return
	}
	if !isPlaylist(body) {
		writeText(w, http.StatusBadGateway, "响应不是 HLS 播放列表")
		return
	}
	rewritten := []byte(rewritePlaylist(target, string(body), s.directSeg))
	s.hls.put(target, playlistContentType, rewritten)
	writeBytes(w, http.StatusOK, playlistContentType, rewritten, "no-store")
}

func (s *Service) serveSegment(ctx context.Context, w http.ResponseWriter, rangeHeader, target string, forceProxy bool) {
	// m3u8 必须由本服务改写后再返回：302 会让 hls.js 跨域拉取清单并失败。
	if isPlaylistTarget(target) {
		s.servePlaylist(ctx, w, target)
		return
	}
	if s.directSeg && !forceProxy && isMediaSegment(target) {
		s.redirectSegment(ctx, w, target)
		return
	}
	if rangeHeader == "" {
		if ctype, data, ok := s.seg.get(target); ok {
			writeBytes(w, http.StatusOK, ctype, data, "public, max-age=3600")
			return
		}
	}
	resp, err := netx.Fetch(ctx, target, rangeHeader)
	if err != nil {
		writeText(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		writeText(w, http.StatusBadGateway, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		writeText(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(body) > maxBodyBytes {
		writeText(w, http.StatusBadGateway, "响应体过大")
		return
	}
	upstreamType := resp.Header.Get("Content-Type")
	// 主清单的子清单也经 /seg 返回，必须继续改写，不能把内层 URL 原样交给播放器。
	if resp.StatusCode == http.StatusOK && isPlaylistWithType(upstreamType, body) {
		writeBytes(w, http.StatusOK, playlistContentType, []byte(rewritePlaylist(target, string(body), s.directSeg)), "public, max-age=3600")
		return
	}
	ctype := segmentContentType(upstreamType, target)
	if resp.StatusCode == http.StatusPartialContent {
		contentRange := resp.Header.Get("Content-Range")
		if contentRange == "" {
			if start, ok := rangeStart(rangeHeader); ok {
				contentRange = fmt.Sprintf("bytes %d-%d/*", start, start+int64(len(body))-1)
			}
		}
		if contentRange != "" {
			w.Header().Set("Content-Range", contentRange)
		}
		writeBytes(w, http.StatusPartialContent, ctype, body, "public, max-age=3600")
		return
	}
	s.seg.put(target, ctype, body)
	writeBytes(w, http.StatusOK, ctype, body, "public, max-age=3600")
}

const playlistContentType = "application/vnd.apple.mpegurl"

// redirectSegment 让播放字节不再经过本服务，只回一个 302 指向上游。
func (s *Service) redirectSegment(ctx context.Context, w http.ResponseWriter, target string) {
	location := target
	if s.resolveRedirect {
		if final, err := finalLocation(ctx, target); err == nil {
			location = final
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

// finalLocation 跟随上游重定向取最终地址；只取响应头，不读响应体。
func finalLocation(ctx context.Context, target string) (string, error) {
	resp, err := netx.Fetch(ctx, target, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.Request == nil || resp.Request.URL == nil {
		return "", fmt.Errorf("缺少最终地址")
	}
	return resp.Request.URL.String(), nil
}

// isPlaylistTarget 判断地址是否为 m3u8（忽略查询串与片段）。
func isPlaylistTarget(target string) bool {
	return strings.HasSuffix(urlPath(target), ".m3u8")
}

// isMediaSegment 判断目标是否可安全 302 的媒体分片。key / init（EXT-X-MAP）等
// 属性资源不在其中：它们由浏览器以 XHR 拉取，跨域会被 CORS 拦住，必须继续代理。
func isMediaSegment(target string) bool {
	switch {
	case strings.HasSuffix(urlPath(target), ".ts"),
		strings.HasSuffix(urlPath(target), ".m4s"),
		strings.HasSuffix(urlPath(target), ".m4a"),
		strings.HasSuffix(urlPath(target), ".mp4"),
		strings.HasSuffix(urlPath(target), ".m4v"),
		strings.HasSuffix(urlPath(target), ".webm"),
		strings.HasSuffix(urlPath(target), ".mkv"),
		strings.HasSuffix(urlPath(target), ".mov"),
		strings.HasSuffix(urlPath(target), ".flv"),
		strings.HasSuffix(urlPath(target), ".aac"),
		strings.HasSuffix(urlPath(target), ".mp3"),
		strings.HasSuffix(urlPath(target), ".vtt"),
		strings.HasSuffix(urlPath(target), ".srt"):
		return true
	}
	return false
}

// urlPath 取去掉查询串与片段的小写路径部分。
func urlPath(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return strings.ToLower(raw)
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func fetchBody(ctx context.Context, target, rangeHeader string) ([]byte, error) {
	resp, err := netx.Fetch(ctx, target, rangeHeader)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("响应体过大")
	}
	return body, nil
}

// queryParam 只做百分号解码，不把 '+' 当空格，与 m3u8 改写用的编码保持对称。
func queryParam(rawQuery, name string) string {
	prefix := name + "="
	for _, part := range strings.Split(rawQuery, "&") {
		if strings.HasPrefix(part, prefix) {
			raw := part[len(prefix):]
			decoded, err := url.PathUnescape(raw)
			if err != nil {
				return raw
			}
			return decoded
		}
	}
	return ""
}

func segmentContentType(upstreamType, target string) string {
	if upstreamType != "" {
		return upstreamType
	}
	path := strings.ToLower(target)
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	switch {
	case strings.HasSuffix(path, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(path, ".m4s"), strings.HasSuffix(path, ".mp4"):
		return "video/mp4"
	}
	return "application/octet-stream"
}

// rangeStart 解析 `bytes=<start>-[end]` 的起点，用于上游漏给 Content-Range 时补齐。
func rangeStart(rangeHeader string) (int64, bool) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(rangeHeader), "bytes=")
	if !ok {
		return 0, false
	}
	first, _, _ := strings.Cut(spec, ",")
	startText, _, ok := strings.Cut(strings.TrimSpace(first), "-")
	if !ok {
		return 0, false
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 {
		return 0, false
	}
	return start, true
}

func imageKind(buf []byte) string {
	if len(buf) < 12 {
		return ""
	}
	switch {
	case buf[0] == 0xFF && buf[1] == 0xD8:
		return "image/jpeg"
	case bytes.HasPrefix(buf, []byte{0x89, 'P', 'N', 'G'}):
		return "image/png"
	case bytes.HasPrefix(buf, []byte("GIF8")):
		return "image/gif"
	case bytes.HasPrefix(buf, []byte("RIFF")) && bytes.Equal(buf[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

func aesCBCDecrypt(data []byte) ([]byte, bool) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, false
	}
	block, err := aes.NewCipher(imgKey)
	if err != nil {
		return nil, false
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, imgIV).CryptBlocks(plain, data)
	return plain, true
}

func stripPadding(buf []byte) []byte {
	if len(buf) == 0 {
		return buf
	}
	pad := int(buf[len(buf)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(buf) {
		return buf
	}
	for _, b := range buf[len(buf)-pad:] {
		if int(b) != pad {
			return buf
		}
	}
	return buf[:len(buf)-pad]
}

func writeBytes(w http.ResponseWriter, status int, ctype string, data []byte, cacheControl string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cacheControl)
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeText(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message)
}