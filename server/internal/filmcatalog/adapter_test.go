package filmcatalog

import (
	"context"
	"encoding/json"
	"fmt"
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

type testSource struct{}

func (*testSource) list(_ context.Context, folder string) ([]entry, error) {
	switch folder {
	case "/影视":
		return []entry{{Name: "影片集.mp4", Dir: true}, {Name: "sample.srt"}, {Name: "top.MKV", Size: 9}}, nil
	case "/影视/影片集.mp4":
		return []entry{{Name: "child.mp4"}, {Name: "../outside.mp4"}}, nil
	default:
		return nil, fmt.Errorf("unexpected directory %q", folder)
	}
}

func (*testSource) link(context.Context, string, string) (link, error) {
	directory := false
	return link{URL: "https://cdn.example.org/play.mp4", Dir: &directory}, nil
}

func TestScanFlattensVideoFiles(t *testing.T) {
	c := catalog{source: &testSource{}, root: "/影视"}
	files, err := c.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 video files, got %+v", files)
	}
	want := map[string]bool{
		"/影视/影片集.mp4/child.mp4": true,
		"/影视/top.MKV":             true,
	}
	for _, file := range files {
		if !want[file.Path] {
			t.Fatalf("unexpected scanned file %+v", file)
		}
		delete(want, file.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing scanned files %v", want)
	}
	for _, file := range files {
		if file.Name == "top.MKV" && file.Size != 9 {
			t.Fatalf("file size not carried over: %+v", file)
		}
	}
}

func TestNewRejectsMissingStore(t *testing.T) {
	if _, err := New("https://openlist.example.org", "/影视", "credentials.json", "cdn.example.org", nil); err == nil {
		t.Fatal("expected an error when no store is provided")
	}
}

func TestNewRejectsInvalidFolder(t *testing.T) {
	if _, err := New("https://openlist.example.org", "影视", "credentials.json", "cdn.example.org", nil); err == nil {
		t.Fatal("expected an error for a non-absolute folder")
	}
}