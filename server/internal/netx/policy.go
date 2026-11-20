package netx

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"unicode"
)

const deniedAddressMsg = "禁止访问普通内网、回环或保留地址（仅放行 FakeIP 198.18.0.0/15）"

var internalHosts = map[string]bool{
	"tauri.localhost": true,
	"hg.localhost":    true,
	"ipc.localhost":   true,
}

// CheckTarget 校验不可信目标：仅允许公网 HTTP(S)，且不带内嵌认证信息。
func CheckTarget(rawURL string) (*url.URL, error) {
	if strings.IndexFunc(rawURL, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("目标地址含控制字符")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("目标地址无效")
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return nil, fmt.Errorf("目标只允许不带认证信息的 HTTP(S) 地址")
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("目标缺少主机名")
	}
	if err := checkHost(host); err != nil {
		return nil, err
	}
	return u, nil
}

func checkHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if !allowedIP(ip) {
			return fmt.Errorf("%s", deniedAddressMsg)
		}
		return nil
	}
	if looksLikeIPLiteral(host) {
		return fmt.Errorf("%s", deniedAddressMsg)
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if internalHosts[name] {
		return fmt.Errorf("禁止访问应用内部协议主机名")
	}
	for _, suffix := range []string{"localhost", "local", "internal", "home.arpa"} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return fmt.Errorf("禁止访问本机或内网主机名")
		}
	}
	return nil
}

// looksLikeIPLiteral 识别 127.1、2130706433、0x7f000001 这类非规范 IPv4 写法，避免被当作域名放行。
func looksLikeIPLiteral(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "" {
		return false
	}
	if strings.HasPrefix(h, "0x") {
		return true
	}
	for _, r := range h {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func allowedIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		a, b, c := v4[0], v4[1], v4[2]
		if a == 198 && (b == 18 || b == 19) {
			return true
		}
		return !(a == 0 ||
			a == 10 ||
			a == 127 ||
			a >= 224 ||
			(a == 100 && b >= 64 && b <= 127) ||
			(a == 169 && b == 254) ||
			(a == 172 && b >= 16 && b <= 31) ||
			(a == 192 && b == 168) ||
			(a == 192 && b == 0 && (c == 0 || c == 2)) ||
			(a == 192 && b == 88 && c == 99) ||
			(a == 198 && b == 51 && c == 100) ||
			(a == 203 && b == 0 && c == 113))
	}
	s := ip.To16()
	if s == nil {
		return false
	}
	first := uint16(s[0])<<8 | uint16(s[1])
	second := uint16(s[2])<<8 | uint16(s[3])
	// 仅全球单播；拒绝 ULA、链路本地、回环、组播、NAT64，以及可封装私网 IPv4 的 6to4/Teredo。
	return (first&0xe000) == 0x2000 &&
		first != 0x2002 &&
		!(first == 0x2001 && (second < 0x0200 || second == 0x0db8)) &&
		!(first == 0x3fff && second < 0x1000)
}

// resolveAndCheck 解析主机并对全部答案做地址检查；混合公私网答案整体拒绝。
func resolveAndCheck(ctx context.Context, host, port string) ([]string, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !allowedIP(ip) {
			return nil, fmt.Errorf("%s", deniedAddressMsg)
		}
		return []string{net.JoinHostPort(ip.String(), port)}, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS 解析失败")
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("目标没有可用的 DNS 地址")
	}
	addrs := make([]string, 0, len(ips))
	for _, entry := range ips {
		if !allowedIP(entry.IP) {
			return nil, fmt.Errorf("%s", deniedAddressMsg)
		}
		addrs = append(addrs, net.JoinHostPort(entry.IP.String(), port))
	}
	sort.SliceStable(addrs, func(i, j int) bool {
		return !isIPv6(addrs[i]) && isIPv6(addrs[j])
	})
	return addrs, nil
}

func isIPv6(hostPort string) bool {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}

// configuredProxyAddr 返回显式配置的代理 host:port。
// 代理是用户主动选择的出口，拨号到代理本身不能再按“目标地址”规则拦截，
// 否则在需要代理出网的环境里所有请求都会失败。
func configuredProxyAddr() string {
	for _, key := range []string{
		"HTTPS_PROXY", "https_proxy",
		"HTTP_PROXY", "http_proxy",
		"ALL_PROXY", "all_proxy",
	} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		host := u.Host
		if u.Port() == "" {
			switch strings.ToLower(u.Scheme) {
			case "https":
				host = net.JoinHostPort(u.Hostname(), "443")
			case "socks5", "socks5h", "socks4", "socks4a":
				host = net.JoinHostPort(u.Hostname(), "1080")
			default:
				host = net.JoinHostPort(u.Hostname(), "80")
			}
		}
		return host
	}
	return ""
}

// safeDialContext 在每个新连接上重新解析并校验目标地址，避免 DNS 重绑定绕过 CheckTarget。
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if proxy := configuredProxyAddr(); proxy != "" && strings.EqualFold(proxy, addr) {
		dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
		return dialer.DialContext(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("目标地址无效")
	}
	addrs, err := resolveAndCheck(ctx, host, port)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
	var lastErr error
	for _, target := range addrs {
		conn, err := dialer.DialContext(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("目标没有可用的 DNS 地址")
	}
	return nil, lastErr
}