// Package commands 把前端 invoke() 映射成 /rpc/{command} 的上游边缘请求。
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/nathanxiangang-web/cilihub/server/internal/media"
	"github.com/nathanxiangang-web/cilihub/server/internal/netx"
	"github.com/nathanxiangang-web/cilihub/server/internal/session"
	"github.com/nathanxiangang-web/cilihub/server/internal/shares"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

const maxBodyBytes = 1 << 20

// Deps 是命令层使用的进程级资源。CatalogEdge 是可选的替代内容源。
// Shares 为空时分享命令返回「分享服务未启用」。
type Deps struct {
	Base        func() string
	SetBase     func(string) (string, error)
	Media       *media.Service
	Log         *log.Logger
	Store       *store.Store
	Shares      *shares.Service
	CatalogEdge func(context.Context, string, string) ([]byte, error)
}

// Register 把 /rpc/{command} 挂到 mux 并注册全部命令。
func Register(mux *http.ServeMux, d Deps) {
	if d.Base == nil || d.SetBase == nil || d.Media == nil || d.Log == nil || d.Store == nil {
		panic("commands: Deps 字段不可为空")
	}
	mux.Handle("/rpc/", &router{deps: d, routes: routes()})
}

type command func(*call) (any, error)

type router struct {
	deps   Deps
	routes map[string]command
}

type call struct {
	h *router
	w http.ResponseWriter
	r *http.Request
	p params
}

func routes() map[string]command {
	out := make(map[string]command, 40)
	// interactRoutes 放在 contentRoutes 之后，用本地落库覆盖同名的边缘转发。
	for _, group := range []map[string]command{authRoutes(), contentRoutes(), miscRoutes(), shareRoutes(), interactRoutes()} {
		for name, fn := range group {
			out[name] = fn
		}
	}
	return out
}

func (h *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/rpc/")
	fn, ok := h.routes[name]
	if name == "" || strings.Contains(name, "/") || !ok {
		writeFail(w, http.StatusNotFound, "unknown command")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeFail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p, err := decodeParams(r)
	if err != nil {
		writeFail(w, http.StatusOK, "请求参数无效")
		return
	}

	c := &call{h: h, w: w, r: r, p: p}
	var (
		value    any
		callErr  error
		panicked bool
	)
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		value, callErr = fn(c)
	}()
	if panicked {
		writeFail(w, http.StatusInternalServerError, "internal error")
		return
	}
	if callErr != nil {
		writeFail(w, http.StatusOK, callErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, value)
}

type errorBody struct {
	Error string `json:"__hg_error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeFail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

type params map[string]any

func decodeParams(r *http.Request) (params, error) {
	p := params{}
	if r.Body == nil {
		return p, nil
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return params{}, nil
		}
		return nil, err
	}
	if p == nil {
		p = params{}
	}
	return p, nil
}

// pick 依次尝试 camelCase / snake_case 键名，忽略显式 null。
func (p params) pick(names ...string) (any, bool) {
	for _, name := range names {
		v, ok := p[name]
		if ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func (p params) str(names ...string) string {
	v, ok := p.pick(names...)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (p params) i64(fallback int64, names ...string) int64 {
	v, ok := p.pick(names...)
	if !ok {
		return fallback
	}
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		if f, err := x.Float64(); err == nil {
			return int64(f)
		}
	case float64:
		return int64(x)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func (p params) flag(names ...string) bool {
	v, ok := p.pick(names...)
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case json.Number:
		n, err := x.Float64()
		return err == nil && n != 0
	case float64:
		return x != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func (p params) strs(names ...string) []string {
	v, ok := p.pick(names...)
	if !ok {
		return nil
	}
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (h *router) token(r *http.Request) string {
	return session.Token(r)
}

// authed 返回当前 token；没有会话时与桌面端一致地报「请先登录」。
func (h *router) authed(r *http.Request) (string, error) {
	token := session.Token(r)
	if token == "" {
		return "", errors.New("请先登录")
	}
	return token, nil
}

// edge 调用上游并原样返回 data，避免重新编码改变字段。
func (h *router) edge(ctx context.Context, method, path string, body any, token string) (json.RawMessage, error) {
	if h.deps.CatalogEdge != nil {
		return h.deps.CatalogEdge(ctx, method, path)
	}
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = encoded
	}
	data, err := netx.Edge(ctx, h.deps.Base(), method, path, payload, token)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(data), nil
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}
