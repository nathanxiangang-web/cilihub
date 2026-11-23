package cloudapi

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

var (
	errProvider      = errors.New("115 请求失败")
	errOutputPending = errors.New("115 结果尚未出现在目标目录")
	idPattern        = regexp.MustCompile("^[1-9][0-9]{0,23}$")
	hostPattern      = regexp.MustCompile("^[a-z0-9.-]+$")
)

const collectorInbox = store.CloudPrivatePrefix + "collector-inbox"

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

func (s *sdk) folder(ctx context.Context, task store.CloudTask) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.Owner == 0 {
		if task.SharedFolder {
			return s.ensure(ctx, s.collectorRoot, collectorInbox)
		}
		return s.ensure(ctx, s.collectorRoot, store.CloudCollectorPrefix+task.Key)
	}
	parent, err := s.ensure(ctx, s.privateRoot, store.CloudPrivatePrefix+strconv.FormatInt(task.Owner, 10))
	if err != nil || task.SharedFolder {
		return parent, err
	}
	return s.ensure(ctx, parent, "task-"+task.Key)
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
				remote.outputID = t.FileId
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

func (s *sdk) files(ctx context.Context, folder, outputID string) ([]store.CloudFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readResults(ctx, folder, outputID)
}

func (s *sdk) readResults(ctx context.Context, folder, outputID string) ([]store.CloudFile, error) {
	if outputID == "" {
		return s.walk(ctx, folder)
	}
	if outputID == folder || !idPattern.MatchString(outputID) {
		return nil, errProvider
	}
	// ListPage reliably reports directory IDs and parents. GetFile may reject folders.
	children, err := s.children(ctx, folder)
	if err != nil {
		return nil, err
	}
	for _, entry := range children {
		if entry.FileID != outputID {
			continue
		}
		if entry.IsDirectory && !isAdvert(entry.Name) {
			return s.walk(ctx, entry.FileID)
		}
		if !entry.IsDirectory && eligibleCloudFile(entry.Name, entry.Size) && entry.PickCode != "" {
			return []store.CloudFile{cloudFile(entry)}, nil
		}
		return nil, nil
	}
	return nil, errOutputPending
}

func cloudFile(f driver.File) store.CloudFile {
	return store.CloudFile{ID: f.FileID, Name: f.Name, Size: f.Size, PickCode: f.PickCode}
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
				if !isAdvert(f.Name) {
					queue = append(queue, f.FileID)
				}
			} else if eligibleCloudFile(f.Name, f.Size) && f.PickCode != "" {
				files = append(files, cloudFile(f))
			}
		}
	}
	return files, nil
}

func (s *sdk) publish(ctx context.Context, task store.CloudTask) ([]store.CloudFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.Owner != 0 || !task.SharedFolder || !idPattern.MatchString(task.OutputID) || task.Folder == s.collectorRoot {
		return nil, errProvider
	}
	destination, err := s.children(ctx, s.collectorRoot)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(destination))
	existing := make(map[string]driver.File, len(destination))
	for _, f := range destination {
		names[strings.ToLower(f.Name)] = true
		existing[f.FileID] = f
	}
	if inbox, ok := existing[task.Folder]; !ok || !inbox.IsDirectory || inbox.Name != collectorInbox {
		return nil, errProvider
	}
	var result []store.CloudFile
	var source map[string]store.CloudFile
	for _, f := range task.Files {
		if entry, ok := existing[f.ID]; ok {
			if entry.IsDirectory || !sameCloudFile(cloudFile(entry), f) {
				return nil, errProvider
			}
			result = append(result, cloudFile(entry))
			continue
		}
		if names[strings.ToLower(f.Name)] {
			continue
		}
		if source == nil {
			files, err := s.readResults(ctx, task.Folder, task.OutputID)
			if err != nil {
				return nil, err
			}
			source = make(map[string]store.CloudFile, len(files))
			for _, item := range files {
				source[item.ID] = item
			}
		}
		if !sameCloudFile(source[f.ID], f) {
			return nil, errProvider
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err := s.client.Move(s.collectorRoot, f.ID); err != nil {
			return nil, errProvider
		}
		moved, err := s.client.GetFile(f.ID)
		if err != nil || moved == nil || moved.IsDirectory || moved.ParentID != s.collectorRoot || !sameCloudFile(cloudFile(*moved), f) {
			return nil, errProvider
		}
		names[strings.ToLower(moved.Name)] = true
		result = append(result, cloudFile(*moved))
	}
	return result, nil
}

func sameCloudFile(entry, f store.CloudFile) bool {
	return entry.ID == f.ID && entry.PickCode == f.PickCode && f.PickCode != "" &&
		entry.Size == f.Size && entry.Name == f.Name && eligibleCloudFile(entry.Name, entry.Size)
}

func (s *sdk) link(ctx context.Context, folder, outputID string, file store.CloudFile, ua string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.readResults(ctx, folder, outputID)
	if err != nil {
		return "", errProvider
	}
	matched := false
	for _, item := range files {
		if sameCloudFile(item, file) {
			matched = true
			break
		}
	}
	if !matched || ctx.Err() != nil {
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
