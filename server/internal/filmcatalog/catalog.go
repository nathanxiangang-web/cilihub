package filmcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type source interface {
	list(context.Context, string) ([]entry, error)
	link(context.Context, string, string) (link, error)
}

type film struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	path  string
}

type snapshot struct {
	films []film
	byID  map[int64]film
}

type catalog struct {
	source  source
	root    string
	current atomic.Pointer[snapshot]
}

func (c *catalog) run(ctx context.Context, logf func(string, ...any)) {
	for ctx.Err() == nil {
		scanCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
		found, err := c.scan(scanCtx)
		cancel()
		delay := 5 * time.Minute
		if err != nil {
			logf("OpenList 目录刷新失败: %v", err)
			delay = 30 * time.Second
		} else {
			c.current.Store(found)
			logf("OpenList 视频目录已刷新: %d 个视频", len(found.films))
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

func (c *catalog) scan(ctx context.Context) (*snapshot, error) {
	const parallelism = 4
	result := &snapshot{films: []film{}, byID: make(map[int64]film)}
	queue := []string{c.root}
	seen := map[string]bool{c.root: true}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count := min(len(queue), parallelism)
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
				if seen[fullPath] {
					continue
				}
				seen[fullPath] = true
				if item.Dir {
					queue = append(queue, fullPath)
					continue
				}
				if !isVideo(item.Name) {
					continue
				}
				digest := sha256.Sum256([]byte(fullPath))
				id := int64(binary.BigEndian.Uint64(digest[:])&((1<<52)-1)) + 1
				if _, collision := result.byID[id]; collision {
					return nil, errors.New("视频标识重复")
				}
				video := film{ID: id, Title: item.Name, path: fullPath}
				result.films = append(result.films, video)
				result.byID[id] = video
			}
		}
	}
	slices.SortFunc(result.films, func(a, b film) int {
		if title := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); title != 0 {
			return title
		}
		return strings.Compare(a.path, b.path)
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
