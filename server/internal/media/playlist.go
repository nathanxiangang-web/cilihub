package media

import (
	"net/url"
	"strings"
)

// rewritePlaylist 把 m3u8 里的 key / 子清单 / 分片地址改写到同源 /media/seg，
// 其余标签原样保留，仅丢弃会派生外部资源的内容引导与广告资源属性。
// absoluteSeg 为真时（302 模式）普通分片与嵌套子清单直接给上游绝对地址，
// 只有 URI="..." 属性仍走 /seg，以便 key / init 等同源返回。
func rewritePlaylist(base, text string, absoluteSeg bool) string {
	text = strings.TrimPrefix(text, "\uFEFF")
	var out strings.Builder
	out.Grow(len(text) + 512)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if line == "" {
			out.WriteByte('\n')
			continue
		}
		if strings.HasPrefix(line, "#") {
			switch {
			case strings.HasPrefix(line, "#EXT-X-CONTENT-STEERING:"):
				continue
			case strings.HasPrefix(line, "#EXT-X-DATERANGE:"):
				if tag, attributes, ok := hlsAttributes(line); ok {
					safe := make([]string, 0, len(attributes))
					for _, attribute := range attributes {
						key := strings.ToUpper(strings.TrimSpace(strings.SplitN(attribute, "=", 2)[0]))
						if strings.Contains(key, "URI") || key == "X-ASSET-LIST" {
							continue
						}
						safe = append(safe, attribute)
					}
					out.WriteString(tag + ":" + strings.Join(safe, ",") + "\n")
				}
				continue
			case strings.Contains(line, `URI="`):
				absolute := mapURIAttributes(line, func(uri string) string { return absolutize(base, uri) })
				out.WriteString(mapURIAttributes(absolute, segmentURI))
			default:
				out.WriteString(line)
			}
		} else {
			out.WriteString(segmentLineURI(base, line, absoluteSeg))
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func segmentURI(absolute string) string {
	// p=1 表示“必须由服务端代理”：KEY / EXT-X-MAP / PART 等属性 URI 体积小，
	// 直连会因跨域被 hls.js 拦下，因此固定走代理。
	return Prefix + "/seg?u=" + encodeURI(absolute) + "&p=1"
}

// segmentLineURI 处理普通行（分片或嵌套子清单）：302 模式给上游绝对地址，
// 代理模式仍包成 /seg?u=。
func segmentLineURI(base, line string, absoluteSeg bool) string {
	absolute := absolutize(base, line)
	if absoluteSeg {
		return absolute
	}
	return segmentURI(absolute)
}

// absolutize 把相对地址按播放列表自身地址解析为绝对地址。
func absolutize(base, target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	b, err := url.Parse(base)
	if err != nil {
		return target
	}
	ref, err := url.Parse(target)
	if err != nil {
		return target
	}
	return b.ResolveReference(ref).String()
}

// hlsAttributes 按未被引号包裹的逗号切分标签属性（URI 值里可能含逗号）。
func hlsAttributes(line string) (string, []string, bool) {
	tag, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", nil, false
	}
	attributes := make([]string, 0, 8)
	quoted := false
	start := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				attributes = append(attributes, rest[start:i])
				start = i + 1
			}
		}
	}
	attributes = append(attributes, rest[start:])
	return tag, attributes, true
}

func mapURIAttributes(line string, rewrite func(string) string) string {
	tag, attributes, ok := hlsAttributes(line)
	if !ok {
		return line
	}
	mapped := make([]string, len(attributes))
	for i, attribute := range attributes {
		mapped[i] = attribute
		key, value, found := strings.Cut(attribute, "=")
		if !found || strings.TrimSpace(key) != "URI" {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if len(trimmed) < 2 || !strings.HasPrefix(trimmed, `"`) || !strings.HasSuffix(trimmed, `"`) {
			continue
		}
		mapped[i] = key + `="` + rewrite(trimmed[1:len(trimmed)-1]) + `"`
	}
	return tag + ":" + strings.Join(mapped, ",")
}

// encodeURI 保留字母与数字，其余全部百分号编码，避免改写后的地址在播放器里被误解析。
func encodeURI(s string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	out.Grow(len(s) * 3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[c>>4])
		out.WriteByte(hex[c&0x0F])
	}
	return out.String()
}

func isPlaylist(body []byte) bool {
	return isPlaylistWithType("", body)
}

func isPlaylistWithType(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "mpegurl") {
		return true
	}
	return hasPlaylistHeader(body)
}

func hasPlaylistHeader(body []byte) bool {
	if len(body) >= 3 && body[0] == 0xEF && body[1] == 0xBB && body[2] == 0xBF {
		body = body[3:]
	}
	return len(body) >= 7 && string(body[:7]) == "#EXTM3U"
}