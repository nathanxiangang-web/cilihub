package filmcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestOpenListObjectHeaderIsPageMetadata(t *testing.T) {
	var item link
	payload := []byte(`{"raw_url":"https://cdn.example.org/play.mp4","is_dir":false,"header":"Folder notes"}`)
	if err := json.Unmarshal(payload, &item); err != nil {
		t.Fatal(err)
	}
	adapter := &Adapter{cdnHost: "cdn.example.org"}
	if _, err := adapter.directURL(item); err != nil {
		t.Fatal(err)
	}
}

type testSource struct {
	linkRequests int
	lastPath     string
	lastAgent    string
	linkHost     string
}

func (*testSource) list(_ context.Context, folder string) ([]entry, error) {
	switch folder {
	case "/影视":
		return []entry{{Name: "影片集.mp4", Dir: true}, {Name: "sample.srt"}, {Name: "top.MKV"}}, nil
	case "/影视/影片集.mp4":
		return []entry{{Name: "child.mp4"}, {Name: "../outside.mp4"}}, nil
	default:
		return nil, fmt.Errorf("unexpected directory %q", folder)
	}
}

func (s *testSource) link(_ context.Context, path, agent string) (link, error) {
	s.linkRequests++
	s.lastAgent, s.lastPath = agent, path
	directory := false
	return link{URL: fmt.Sprintf("https://%s/play.mp4?request=%d", s.linkHost, s.linkRequests), Dir: &directory}, nil
}

func TestVideoCatalogRedirect(t *testing.T) {
	source := &testSource{linkHost: "cdn.example.org"}
	adapter := &Adapter{catalog: catalog{source: source, root: "/影视"}, cdnHost: source.linkHost}
	snapshot, err := adapter.catalog.scan(context.Background())
	if err != nil || len(snapshot.films) != 2 || snapshot.films[0].Title != "child.mp4" || snapshot.films[1].Title != "top.MKV" {
		t.Fatalf("flattened video catalog incorrect: %+v, %v", snapshot, err)
	}
	adapter.catalog.current.Store(snapshot)
	feed, err := adapter.Edge(context.Background(), http.MethodGet, "/api/videos?page=1&size=1")
	if err != nil || !json.Valid(feed) {
		t.Fatalf("cilihub feed failed: %v", err)
	}
	firstVideoID := strconv.FormatInt(snapshot.films[0].ID, 10)
	detail, err := adapter.Edge(context.Background(), http.MethodGet, "/api/videos/"+firstVideoID)
	if err != nil || !json.Valid(detail) {
		t.Fatalf("cilihub video details failed: %v", err)
	}
	play, err := adapter.Edge(context.Background(), http.MethodPost, "/api/videos/"+firstVideoID+"/episodes/1/playback")
	var playback struct {
		URL string `json:"video_url"`
	}
	if err != nil || json.Unmarshal(play, &playback) != nil || playback.URL != MediaPrefix+firstVideoID {
		t.Fatalf("cilihub playback failed: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		request := httptest.NewRequest(http.MethodGet, MediaPrefix+strconv.FormatInt(snapshot.films[0].ID, 10), nil)
		request.Header.Set("User-Agent", "MovieHub Browser Test")
		response := httptest.NewRecorder()
		adapter.ServeHTTP(response, request)
		expected := "https://cdn.example.org/play.mp4?request=" + strconv.Itoa(attempt)
		if response.Code != 302 || response.Header().Get("Location") != expected || response.Body.Len() != 0 ||
			response.Header().Get("Cache-Control") != "no-store" || source.lastAgent != "MovieHub Browser Test" || source.lastPath != "/影视/影片集.mp4/child.mp4" {
			t.Fatal("playback did not mint and return a fresh bodyless redirect")
		}
	}
	source.linkHost = "other.example.org"
	response := httptest.NewRecorder()
	adapter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, MediaPrefix+strconv.FormatInt(snapshot.films[0].ID, 10), nil))
	if response.Code != 502 || response.Header().Get("Location") != "" {
		t.Fatal("unexpected redirect outside the configured CDN")
	}
}
