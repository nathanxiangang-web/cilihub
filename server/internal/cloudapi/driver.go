package cloudapi

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

var (
	errProvider = errors.New("115 请求失败")
	idPattern   = regexp.MustCompile("^[1-9][0-9]{0,23}$")
	hostPattern = regexp.MustCompile("^[a-z0-9.-]+$")
)

func newService(db taskStore, cfg Config, logger *log.Logger) (*Service, error) {
	s := &Service{db: db, logger: logger}
	cfg.PrivateRoot = strings.TrimSpace(cfg.PrivateRoot)
	cfg.CollectorRoot = strings.TrimSpace(cfg.CollectorRoot)
	if cfg.PrivateRoot == "" && cfg.CollectorRoot == "" {
		return s, nil
	}
	if cfg.PrivateRoot != "" && !idPattern.MatchString(cfg.PrivateRoot) ||
		cfg.CollectorRoot != "" && !idPattern.MatchString(cfg.CollectorRoot) ||
		cfg.PrivateRoot == cfg.CollectorRoot {
		return nil, errors.New("115 私人空间与运营空间必须使用各自的 115 目录 ID")
	}
	if cfg.CollectorRoot != "" {
		data, err := os.ReadFile(cfg.CollectorTokenFile)
		if err != nil || len(strings.TrimSpace(string(data))) < 32 {
			return nil, errors.New("运营下载需要至少 32 字符的内部令牌")
		}
		s.collectorKey = []byte(strings.TrimSpace(string(data)))
	}
	raw, err := os.ReadFile(cfg.CookieFile)
	credential := &driver.Credential{}
	if err != nil || credential.FromCookie(strings.TrimSpace(string(raw))) != nil {
		return nil, errors.New("无法加载 115 凭据文件")
	}
	cdn := strings.ToLower(strings.TrimSpace(cfg.CDNHost))
	if cdn == "" {
		cdn = "cdnfhnfile.115cdn.net"
	}
	if !hostPattern.MatchString(cdn) || !strings.HasSuffix(cdn, ".115cdn.net") {
		return nil, errors.New("115 CDN 主机必须是 115cdn.net 的子域名")
	}
	client := &http.Client{
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	s.source = &sdk{
		client:      driver.New(driver.WithClient(client), driver.UA(driver.UA115Browser)).ImportCredential(credential),
		privateRoot: cfg.PrivateRoot, collectorRoot: cfg.CollectorRoot, cdn: cdn,
	}
	s.private = cfg.PrivateRoot != ""
	return s, nil
}

type sdk struct {
	client                          *driver.Pan115Client
	privateRoot, collectorRoot, cdn string
	mu                              sync.Mutex
}

func (s *sdk) folder(ctx context.Context, owner int64, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == 0 {
		return s.ensure(ctx, s.collectorRoot, store.CloudCollectorPrefix+key)
	}
	parent, err := s.ensure(ctx, s.privateRoot, store.CloudPrivatePrefix+strconv.FormatInt(owner, 10))
	if err != nil {
		return "", err
	}
	return s.ensure(ctx, parent, "task-"+key)
}

func (s *sdk) ensure(ctx context.Context, parent, name string) (string, error) {
	files, err := s.children(ctx, parent)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.Name == name && f.IsDirectory {
			return f.FileID, nil
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	id, err := s.client.Mkdir(parent, name)
	if err != nil || !idPattern.MatchString(id) {
		return "", errProvider
	}
	return id, nil
}

func (s *sdk) children(ctx context.Context, parent string) ([]driver.File, error) {
	if !idPattern.MatchString(parent) {
		return nil, errProvider
	}
	var files []driver.File
	const limit int64 = 200
	for offset := int64(0); offset < 200000; {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		page, err := s.client.ListPage(parent, offset, limit)
		if err != nil || page == nil {
			return nil, errProvider
		}
		for _, f := range *page {
			if f.ParentID != parent || !idPattern.MatchString(f.FileID) {
				return nil, errProvider
			}
			files = append(files, f)
		}
		if len(*page) == 0 {
			return files, nil
		}
		offset += int64(len(*page))
	}
	return nil, errProvider
}

func (s *sdk) find(ctx context.Context, hash, folder string) (*remoteTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var foreign bool
	for page := int64(1); page <= 1000; page++ {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		result, err := s.client.ListOfflineTask(page)
		if err != nil {
			return nil, false, errProvider
		}
		for _, t := range result.Tasks {
			if t == nil || !strings.EqualFold(t.InfoHash, hash) {
				continue
			}
			if t.DirId != folder {
				foreign = true
				continue
			}
			remote := &remoteTask{}
			switch {
			case t.IsTodo():
				remote.state = "provider_queued"
			case t.IsRunning():
				remote.state = "downloading"
				if !math.IsNaN(t.Percent) && t.Percent >= 0 && t.Percent <= 100 {
					remote.percent = &t.Percent
				}
			case t.IsDone():
				remote.state = "done"
				percent := 100.0
				remote.percent = &percent
			case t.IsFailed():
				remote.state = "failed"
			}
			return remote, false, nil
		}
		if page >= result.PageCount {
			return nil, foreign, nil
		}
	}
	return nil, false, errProvider
}

func (s *sdk) submit(ctx context.Context, magnet, folder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, hash, err := normalizeMagnet(magnet)
	if err != nil || !idPattern.MatchString(folder) {
		return errProvider
	}
	hashes, err := s.client.AddOfflineTaskURIs([]string{magnet}, folder)
	if err != nil || len(hashes) != 1 || !strings.EqualFold(hashes[0], hash) {
		return errProvider
	}
	return nil
}

func (s *sdk) files(ctx context.Context, folder string) ([]store.CloudFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.walk(ctx, folder)
}

func (s *sdk) walk(ctx context.Context, folder string) ([]store.CloudFile, error) {
	queue := []string{folder}
	seen := map[string]bool{folder: true}
	var files []store.CloudFile
	for len(queue) > 0 {
		children, err := s.children(ctx, queue[0])
		if err != nil {
			return nil, err
		}
		queue = queue[1:]
		for _, f := range children {
			if seen[f.FileID] {
				continue
			}
			seen[f.FileID] = true
			if f.IsDirectory {
				queue = append(queue, f.FileID)
			} else if strings.EqualFold(path.Ext(f.Name), ".mp4") && f.PickCode != "" {
				files = append(files, store.CloudFile{ID: f.FileID, Name: f.Name, Size: f.Size, PickCode: f.PickCode})
			}
		}
	}
	return files, nil
}

func (s *sdk) link(ctx context.Context, folder string, file store.CloudFile, ua string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// Revalidate the current file and its entire parent chain before redirecting.
	id := file.ID
	for depth := 0; depth < 64 && id != folder; depth++ {
		if !idPattern.MatchString(id) || ctx.Err() != nil {
			return "", errProvider
		}
		entry, err := s.client.GetFile(id)
		if err != nil || entry == nil || entry.FileID != id ||
			(depth == 0 && (entry.IsDirectory || entry.PickCode != file.PickCode || !strings.EqualFold(path.Ext(entry.Name), ".mp4"))) ||
			(depth > 0 && !entry.IsDirectory) {
			return "", errProvider
		}
		id = entry.ParentID
	}
	if id != folder || id == file.ID || ctx.Err() != nil {
		return "", errProvider
	}
	download, err := s.client.DownloadWithUA(file.PickCode, ua)
	if err != nil || download == nil || !download.Url.Valid {
		return "", errProvider
	}
	link, err := url.Parse(download.Url.Url)
	if err != nil || link.Scheme != "https" || link.Host != s.cdn || link.User != nil {
		return "", errProvider
	}
	return download.Url.Url, nil
}
