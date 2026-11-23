package cloudapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
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

func TestSharedFolderReadsOnlyTaskOutput(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		var payload string
		switch q.Get("cid") {
		case "101":
			if q.Get("offset") == "0" {
				payload = fmt.Sprintf(`{"state":true,"cid":"101","count":2,"data":[{"cid":"201","pid":"101","n":"第一部"},
{"fid":"202","cid":"101","n":"第二部.mp4","pc":"second","s":%d}]}`, minCloudFileBytes)
			} else {
				payload = `{"state":true,"cid":"101","count":2,"data":[]}`
			}
		case "201":
			if q.Get("offset") == "0" {
				payload = fmt.Sprintf(`{"state":true,"cid":"201","count":5,"data":[
{"fid":"401","cid":"201","n":"第一部.MP4","pc":"first","s":%d},
{"fid":"402","cid":"201","n":"preview.mp4","pc":"small","s":%d},
{"fid":"403","cid":"201","n":"广告.mp4","pc":"ad","s":%d},
{"fid":"404","cid":"201","n":"字幕.srt","s":%d},
{"cid":"405","pid":"201","n":"广告"}]}`, minCloudFileBytes, minCloudFileBytes-1, minCloudFileBytes, minCloudFileBytes)
			} else {
				payload = `{"state":true,"cid":"201","count":5,"data":[]}`
			}
		default:
			t.Fatalf("unrelated folders must not be queried: %v", q)
		}
		return jsonReply(r, payload), nil
	})
	s := &sdk{client: driver.New(driver.WithClient(&http.Client{Transport: transport}))}
	for _, tc := range []struct{ output, file string }{{"201", "401"}, {"202", "202"}} {
		files, err := s.files(context.Background(), "101", tc.output)
		if err != nil || len(files) != 1 || files[0].ID != tc.file {
			t.Errorf("incorrect output for %s: %+v, %v", tc.output, files, err)
		}
	}
	for _, bad := range []string{"101", "301"} {
		if _, err := s.files(context.Background(), "101", bad); err == nil {
			t.Errorf("accepted unscoped output %s", bad)
		}
	}
}

func TestNewTasksUseFixedFolders(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.Method != http.MethodGet {
			t.Fatalf("existing fixed folders should not cause writes: %s", r.Method)
		}
		var data string
		switch q.Get("cid") {
		case "500":
			data = fmt.Sprintf(`{"cid":"501","pid":"500","n":"%s"}`, collectorInbox)
		case "600":
			data = fmt.Sprintf(`{"cid":"601","pid":"600","n":"%s42"}`, store.CloudPrivatePrefix)
		default:
			t.Fatalf("unexpected lookup of a task-specific folder: %v", q)
		}
		if q.Get("offset") != "0" {
			data = ""
		}
		return jsonReply(r, fmt.Sprintf(`{"state":true,"cid":"%s","count":1,"data":[%s]}`, q.Get("cid"), data)), nil
	})
	s := &sdk{client: driver.New(driver.WithClient(&http.Client{Transport: transport})), collectorRoot: "500", privateRoot: "600"}
	for _, tc := range []struct {
		owner int64
		want  string
	}{{42, "601"}, {0, "501"}} {
		for _, key := range []string{"first", "second"} {
			got, err := s.folder(context.Background(), store.CloudTask{Owner: tc.owner, Key: key, SharedFolder: true})
			if err != nil || got != tc.want {
				t.Fatalf("folder for owner %d: got %q, want %q (%v)", tc.owner, got, tc.want, err)
			}
		}
	}
}

func TestCollectorResumesMoveAndSkipsNameCollision(t *testing.T) {
	moved := false
	moves := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil || r.Form.Get("pid") != "500" || r.Form.Get("fid[0]") != "201" {
				t.Fatalf("unexpected move: %v, %v", r.Form, err)
			}
			moved = true
			moves++
			return jsonReply(r, `{"state":false,"error":"lost response"}`), nil
		}
		q := r.URL.Query()
		if q.Get("cid") == "101" && !moved {
			data := ""
			if q.Get("offset") == "0" {
				data = fmt.Sprintf(`{"fid":"201","cid":"101","n":"video.mp4","pc":"move","s":%d}`, minCloudFileBytes)
			}
			return jsonReply(r, fmt.Sprintf(`{"state":true,"cid":"101","count":1,"data":[%s]}`, data)), nil
		}
		if q.Get("file_id") == "201" {
			parent := "101"
			if moved {
				parent = "500"
			}
			return jsonReply(r, fmt.Sprintf(`{"state":true,"data":[{"fid":"201","cid":"%s","n":"video.mp4","pc":"move","s":%d}]}`, parent, minCloudFileBytes)), nil
		}
		if q.Get("cid") != "500" {
			t.Fatalf("unexpected lookup: %v", q)
		}
		data := fmt.Sprintf(`{"cid":"101","pid":"500","n":"%s"},{"fid":"303","cid":"500","n":"DUPLICATE.mp4","pc":"other","s":%d}`, collectorInbox, minCloudFileBytes)
		if moved {
			data += fmt.Sprintf(`,{"fid":"201","cid":"500","n":"video.mp4","pc":"move","s":%d}`, minCloudFileBytes)
		}
		if q.Get("offset") != "0" {
			data = ""
		}
		count := 2
		if moved {
			count = 3
		}
		return jsonReply(r, fmt.Sprintf(`{"state":true,"cid":"500","count":%d,"data":[%s]}`, count, data)), nil
	})
	s := &sdk{client: driver.New(driver.WithClient(&http.Client{Transport: transport})), collectorRoot: "500"}
	task := store.CloudTask{SharedFolder: true, Folder: "101", OutputID: "201", Files: []store.CloudFile{
		{ID: "304", PickCode: "untouched", Name: "duplicate.mp4", Size: minCloudFileBytes},
		{ID: "201", PickCode: "move", Name: "video.mp4", Size: minCloudFileBytes},
	}}
	if _, err := s.publish(context.Background(), task); err == nil {
		t.Fatal("missing move response should leave the manifest available for retry")
	}
	files, err := s.publish(context.Background(), task)
	if err != nil || moves != 1 || len(files) != 1 || files[0].ID != "201" {
		t.Fatalf("unexpected retry result: %+v, %v (%d moves)", files, err, moves)
	}
}

func jsonReply(r *http.Request, data string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(data)), Request: r}
}
