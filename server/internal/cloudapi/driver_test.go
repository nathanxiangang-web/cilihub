package cloudapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SheltonZhu/115driver/pkg/driver"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderRequiresExactFolder(t *testing.T) {
	hash := strings.Repeat("a", 40)
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var task string
		switch r.URL.Query().Get("page") {
		case "1":
			task = `{"info_hash":"` + hash + `","wp_path_id":"111","status":2,"percentDone":100}`
		case "2":
			task = `{"info_hash":"` + strings.ToUpper(hash) + `","wp_path_id":"222","status":1,"percentDone":57.5}`
		default:
			t.Fatalf("unexpected offline page: %s", r.URL.Query().Get("page"))
		}
		payload := `{"state":true,"page_count":2,"tasks":[` + task + `]}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})
	s := &sdk{client: driver.New(driver.WithClient(&http.Client{Transport: transport}))}
	task, foreign, err := s.find(context.Background(), hash, "222")
	if err != nil || foreign || task == nil || task.state != "downloading" || task.percent == nil || *task.percent != 57.5 || calls != 2 {
		t.Fatalf("progress from exact folder missing: %+v, %v, %v (%d requests)", task, foreign, err, calls)
	}
	task, foreign, err = s.find(context.Background(), hash, "333")
	if err != nil || !foreign || task != nil {
		t.Fatalf("accepted unrelated folder: %+v, %v, %v", task, foreign, err)
	}
}
