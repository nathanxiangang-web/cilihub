package cloudapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
	"github.com/nathanxiangang-web/cilihub/server/internal/userauth"
)

type test115 struct {
	submissions int
	matched     bool
	state       string
	output      []store.CloudFile
}

func (p *test115) folder(_ context.Context, owner int64, key string) (string, error) {
	return strconv.FormatInt(owner, 10) + "/" + key, nil
}
func (p *test115) find(_ context.Context, _, _ string) (*remoteTask, bool, error) {
	if !p.matched {
		return nil, false, nil
	}
	progress := 63.5
	return &remoteTask{state: p.state, percent: &progress}, false, nil
}
func (p *test115) submit(_ context.Context, _, _ string) error {
	p.submissions++
	return errors.New("simulated lost response")
}
func (p *test115) files(_ context.Context, _ string) ([]store.CloudFile, error) { return p.output, nil }
func (p *test115) link(_ context.Context, folder string, file store.CloudFile, _ string) (string, error) {
	if folder == "" || file.PickCode == "" {
		return "", errors.New("missing provider file")
	}
	return "https://test.115cdn.net/signed-video", nil
}

func TestCloudDownloadFlow(t *testing.T) {
	dsn := os.Getenv("CILIHUB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL database")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mux := http.NewServeMux()
	service, err := Register(mux, db, Config{}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	provider := &test115{}
	service.source = provider
	service.private = true
	service.collectorKey = []byte("synthetic-collector-token-for-testing")

	now := time.Now().UTC()
	cookie := func(name string) *http.Cookie {
		t.Helper()
		user, err := db.CreateUser(name, "unused-test-hash", "", now.Format(time.RFC3339))
		if err != nil {
			t.Fatal(err)
		}
		token, hash := userauth.NewToken()
		if err := db.PutUserSession(hash, user.ID, now.Add(time.Hour).Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		header := httptest.NewRecorder()
		userauth.SetCookie(header, httptest.NewRequest("GET", "http://localhost/", nil), token)
		return header.Result().Cookies()[0]
	}
	first, second := cookie("cloud-test-a-"+strconv.FormatInt(now.UnixNano(), 10)), cookie("cloud-test-b-"+strconv.FormatInt(now.UnixNano(), 10))
	call := func(method, path, body string, session *http.Cookie, key bool, expected int) *httptest.ResponseRecorder {
		t.Helper()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if session != nil {
			req.AddCookie(session)
		}
		if key {
			req.Header.Set("Authorization", "Bearer "+string(service.collectorKey))
		}
		mux.ServeHTTP(rr, req)
		if rr.Code != expected {
			t.Fatalf("%s %s: got %d, want %d (%s)", method, path, rr.Code, expected, rr.Body)
		}
		return rr
	}
	body := `{"magnet":"magnet:?xt=urn:btih:` + strings.Repeat("1", 40) + `&dn=Demo"}`
	call("GET", privatePath, "", nil, false, 401)
	resp := call("POST", privatePath, body, first, false, 202)
	var created struct{ Task taskView }
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil || created.Task.ID <= 0 {
		t.Fatal("missing task", err)
	}
	id := strconv.FormatInt(created.Task.ID, 10)
	play := privatePath + "/" + id + "/files/0"
	call("POST", privatePath, body, first, false, 200)
	call("GET", play, "", second, false, 404)
	call("POST", collectorPath, body, first, false, 401)
	if rr := call("GET", privatePath, "", second, false, 200); !strings.Contains(rr.Body.String(), `"tasks":[]`) {
		t.Fatal("another owner saw a task")
	}

	service.tick(context.Background())
	service.tick(context.Background())
	if rr := call("GET", privatePath, "", first, false, 200); !strings.Contains(rr.Body.String(), `"state":"uncertain"`) {
		t.Fatal(rr.Body.String())
	}
	if provider.submissions != 1 {
		t.Fatalf("uncertain POST was replayed: %d", provider.submissions)
	}

	provider.matched = true
	provider.state = "downloading"
	service.tick(context.Background())
	if rr := call("GET", privatePath, "", first, false, 200); !strings.Contains(rr.Body.String(), `"progress":63.5`) {
		t.Fatal(rr.Body.String())
	}
	provider.state = "done"
	provider.output = []store.CloudFile{{ID: "11", Name: "story.MP4", PickCode: "provider-secret"}, {Name: "notes.txt"}}
	service.tick(context.Background())
	list := call("GET", privatePath, "", first, false, 200).Body.String()
	if !strings.Contains(list, "story.MP4") || strings.Contains(list, "notes.txt") || strings.Contains(list, "provider-secret") {
		t.Fatal(list)
	}
	if rr := call("GET", play, "", first, false, 302); rr.Header().Get("Location") != "https://test.115cdn.net/signed-video" ||
		rr.Body.Len() != 0 || rr.Header().Get("Set-Cookie") != "" {
		t.Fatal("play must return only an upstream redirect")
	}
	call("GET", play, "", second, false, 404)
	call("GET", collectorPath+"/"+id, "", nil, true, 404)

	call("POST", privatePath, strings.Replace(body, strings.Repeat("1", 40), strings.Repeat("2", 40), 1), second, false, 202)
	provider.output = []store.CloudFile{{Name: "notes.txt"}}
	service.tick(context.Background())
	if rr := call("GET", privatePath, "", second, false, 200); !strings.Contains(rr.Body.String(), `"state":"no_mp4"`) {
		t.Fatal(rr.Body.String())
	}
}

func TestMagnetBTIH(t *testing.T) {
	const zeros = "0000000000000000000000000000000000000000"
	_, hash, err := normalizeMagnet("magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil || hash != zeros {
		t.Fatalf("base32 BTIH failed: %q, %v", hash, err)
	}
	if _, _, err := normalizeMagnet("magnet:?xt=urn:btih:" + zeros + "&xt=urn:btih:" + strings.Repeat("1", 40)); err == nil {
		t.Fatal("accepted conflicting hashes")
	}
}
