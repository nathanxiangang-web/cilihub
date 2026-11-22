package cloudapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

func TestMagSearchOwnerAndResults(t *testing.T) {
	const key = "synthetic-magcraw-api-key-for-testing"
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == magJobs:
			var body struct{ Keywords []string }
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Keywords) != 1 || body.Keywords[0] != "测试影片" {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(202)
			fmt.Fprint(w, `{"job_id":"j123","state":"running"}`)
		case r.Method == "GET" && r.URL.Path == magJobs+"/j123/results":
			fmt.Fprintf(w, `{"job_id":"j123","state":"done","items":[{"title":"测试影片","magnet":%q},{"magnet":%q},{"magnet":"https://example.test"}]}`, magnet, magnet)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	search, err := newMagSearch(upstream.URL, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	s := Service{magnets: search}
	mux := http.NewServeMux()
	s.register(mux, func(r *http.Request) (int64, error) {
		if value := r.Header.Get("Test-Owner"); value != "" {
			return strconv.ParseInt(value, 10, 64)
		}
		return 0, store.ErrNotFound
	})
	call := func(method, path, body, owner string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-Owner", owner)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("%s %s: %d != %d (%s)", method, path, out.Code, want, out.Body)
		}
		return out
	}
	path := privatePath + "/search"
	body := `{"query":"测试影片"}`
	call("POST", path, body, "", 401)
	created := call("POST", path, body, "10", 202)
	var handle struct{ Key string }
	if err := json.Unmarshal(created.Body.Bytes(), &handle); err != nil || handle.Key == "" || handle.Key == "j123" {
		t.Fatal("invalid search handle")
	}
	call("GET", path+"/"+handle.Key, "", "11", 404)
	results := call("GET", path+"/"+handle.Key, "", "10", 200)
	var data magResponse
	if err := json.Unmarshal(results.Body.Bytes(), &data); err != nil || data.State != "done" || len(data.Items) != 1 || data.Items[0].Title != "测试影片" {
		t.Fatalf("incorrect normalized results: %s", results.Body)
	}
}
