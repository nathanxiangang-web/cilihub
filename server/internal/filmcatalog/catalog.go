package filmcatalog

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

const (
	scanInterval    = 5 * time.Minute
	retryInterval   = 30 * time.Second
	scanTimeout     = 6 * time.Minute
	scanParallelism = 4
)

type source interface {
	list(context.Context, string) ([]entry, error)
	link(context.Context, string, string) (link, error)
}

type catalog struct {
	source source
	root   string
	store  *store.Store
	mu     sync.Mutex
}

func (c *catalog) run(ctx context.Context, logf func(string, ...any)) {
	for ctx.Err() == nil {
		delay := scanInterval
		if !c.ready() {
			// 尚未配置源时不写 sync_runs，等待后台 Apply
			delay = retryInterval
		} else if runID, err := c.sync(ctx); err != nil {
			logf("OpenList 目录刷新失败: %v", err)
			delay = retryInterval
		} else {
			logf("OpenList 视频目录已合并入库（同步 #%d）", runID)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *catalog) ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.source != nil
}

func (c *catalog) syncNow() (int64, error) {
	return c.sync(context.Background())
}

func (c *catalog) sync(ctx context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.source == nil {
		return 0, errors.New("尚未配置 OpenList 连接")
	}
	scanCtx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	runID, err := c.store.StartSyncRun(nowText())
	if err != nil {
		return 0, err
	}
	files, err := c.scan(scanCtx)
	if err != nil {
		c.fail(runID, err)
		return runID, err
	}
	merged, err := c.store.ApplyScan(runID, files, nowText())
	if err != nil {
		c.fail(runID, err)
		return runID, err
	}
	if err := c.store.FinishSyncRun(runID, "ok", merged.Added, merged.Missing, 0, ""); err != nil {
		return runID, err
	}
	return runID, nil
}

// fail 只终结本次同步记录；扫描失败时数据库停留在上次成功状态。
func (c *catalog) fail(runID int64, cause error) {
	_ = c.store.FinishSyncRun(runID, "failed", 0, 0, 1, cause.Error())
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339) }

func (c *catalog) scan(ctx context.Context) ([]store.ScannedFile, error) {
	if store.IsCloudPrivatePath(c.root) {
		return nil, errors.New("OpenList 公开目录不能指向私人云下载目录")
	}
	result := make([]store.ScannedFile, 0)
	queue := []string{c.root}
	seen := map[string]bool{c.root: true}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count := min(len(queue), scanParallelism)
		folders := queue[:count:count]
		queue = queue[count:]
		read := make([]struct {
			entries []entry
			err     error
		}, count)
		var group sync.WaitGroup
		for i, folder := range folders {
			group.Add(1)
			go func() {
				defer group.Done()
				read[i].entries, read[i].err = c.source.list(ctx, folder)
			}()
		}
		group.Wait()
		for i, folder := range folders {
			if read[i].err != nil {
				return nil, read[i].err
			}
			for _, item := range read[i].entries {
				if item.Name == "" || item.Name == "." || item.Name == ".." || strings.ContainsAny(item.Name, "/\\\x00") {
					continue
				}
				fullPath := path.Join(folder, item.Name)
				if seen[fullPath] || store.IsCloudPrivatePath(fullPath) {
					continue
				}
				seen[fullPath] = true
				if item.Dir {
					queue = append(queue, fullPath)
					continue
				}
				if !isVideo(item.Name) || (store.IsCloudCollectorPath(fullPath) && !strings.EqualFold(path.Ext(item.Name), ".mp4")) {
					continue
				}
				result = append(result, store.ScannedFile{Path: fullPath, Name: item.Name, Size: item.Size})
			}
		}
	}
	slices.SortFunc(result, func(a, b store.ScannedFile) int {
		return strings.Compare(a.Path, b.Path)
	})
	return result, nil
}

func isVideo(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".m4v", ".webm", ".mov", ".mkv", ".avi", ".ts", ".m2ts", ".flv", ".wmv", ".mpg", ".mpeg", ".rmvb", ".3gp", ".ogv":
		return true
	default:
		return false
	}
}
