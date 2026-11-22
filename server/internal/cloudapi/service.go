// Package cloudapi owns private download jobs and the authenticated collector intake.
package cloudapi

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/nathanxiangang-web/cilihub/server/internal/store"
)

type taskStore interface {
	QueueCloud(int64, string, string, string, string) (store.CloudTask, bool, error)
	CloudTask(int64, int64) (store.CloudTask, error)
	CloudTasks(int64, int64) ([]store.CloudTask, error)
	PendingCloud() ([]store.CloudTask, error)
	UpdateCloud(store.CloudTask, string, string) (bool, error)
}

type remoteTask struct {
	state   string
	percent *float64
}
type provider interface {
	folder(context.Context, int64, string) (string, error)
	find(context.Context, string, string) (*remoteTask, bool, error)
	submit(context.Context, string, string) error
	files(context.Context, string) ([]store.CloudFile, error)
	link(context.Context, string, store.CloudFile, string) (string, error)
}

type Service struct {
	db           taskStore
	source       provider
	magnets      *magSearch
	private      bool
	collectorKey []byte
	logger       *log.Logger
}

func normalizeMagnet(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	u, err := url.Parse(value)
	bad := errors.New("请输入包含有效 BTIH 的磁力链接")
	if err != nil || len(value) > 8192 || u.Scheme != "magnet" || u.Opaque != "" || u.Host != "" || u.Path != "" || u.Fragment != "" {
		return "", "", bad
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", "", bad
	}
	var hash string
	for _, xt := range query["xt"] {
		if !strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
			continue
		}
		raw := xt[len("urn:btih:"):]
		var decoded []byte
		switch len(raw) {
		case 40:
			decoded, err = hex.DecodeString(raw)
		case 32:
			decoded, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(raw))
		default:
			return "", "", bad
		}
		if err != nil || len(decoded) != 20 {
			return "", "", bad
		}
		canonical := hex.EncodeToString(decoded)
		if hash != "" && hash != canonical {
			return "", "", bad
		}
		hash = canonical
	}
	if hash == "" {
		return "", "", bad
	}
	return value, hash, nil
}

func (s *Service) Run(ctx context.Context) {
	if s.source == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	tasks, err := s.db.PendingCloud()
	if err != nil {
		s.logger.Print("云下载读取队列失败")
		return
	}
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		if task.Owner == 0 && len(s.collectorKey) == 0 || task.Owner > 0 && !s.private {
			continue
		}
		s.advance(ctx, task)
	}
}

func (s *Service) update(task store.CloudTask, next, message string) bool {
	updated, err := s.db.UpdateCloud(task, next, message)
	if err != nil {
		s.logger.Printf("记录云下载任务 %d 状态失败", task.ID)
	}
	return err == nil && updated
}

func (s *Service) advance(ctx context.Context, task store.CloudTask) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if task.State == "queued" {
		folder, err := s.source.folder(ctx, task.Owner, task.Key)
		if err != nil {
			s.update(task, "queued", "等待 115 创建下载目录")
			return
		}
		task.Folder = folder
		found, foreign, err := s.source.find(ctx, task.Hash, folder)
		if err != nil {
			s.update(task, "queued", "等待 115 任务查询恢复")
			return
		}
		if found != nil {
			s.applyRemote(ctx, task, found)
			return
		}
		if foreign {
			s.update(task, "failed", "115 已有相同任务，下载目录与本任务不符")
			return
		}
		// The persisted phase fences the POST across timeouts and restarts.
		if !s.update(task, "submitting", "正在提交给 115") {
			return
		}
		task.State = "submitting"
		if err = s.source.submit(ctx, task.Magnet, folder); err != nil {
			s.update(task, "uncertain", "提交结果待核对，系统会继续检查 115 任务")
			return
		}
		s.update(task, "provider_queued", "已提交，请过几分钟刷新")
		return
	}
	if task.State == "indexing" {
		s.index(ctx, task)
		return
	}
	remote, _, err := s.source.find(ctx, task.Hash, task.Folder)
	if err != nil {
		s.update(task, task.State, "115 状态暂时无法读取，稍后刷新")
		return
	}
	if remote == nil {
		s.update(task, "uncertain", "等待核对与当前下载目录匹配的 115 任务")
		return
	}
	s.applyRemote(ctx, task, remote)
}

func (s *Service) applyRemote(ctx context.Context, task store.CloudTask, remote *remoteTask) {
	task.Progress = remote.percent
	switch remote.state {
	case "done":
		if s.update(task, "indexing", "115 下载完成，正在读取 MP4") {
			task.State = "indexing"
			s.index(ctx, task)
		}
	case "failed":
		s.update(task, "failed", "115 下载失败")
	case "downloading":
		s.update(task, "downloading", "115 下载中")
	case "provider_queued":
		s.update(task, "provider_queued", "等待 115 开始下载")
	default:
		s.update(task, "uncertain", "115 返回未知状态，等待下一次核对")
	}
}

func (s *Service) index(ctx context.Context, task store.CloudTask) {
	files, err := s.source.files(ctx, task.Folder)
	if err != nil {
		s.update(task, "indexing", "暂时无法读取下载结果，稍后刷新")
		return
	}
	task.Files = []store.CloudFile{}
	for _, file := range files {
		if strings.HasSuffix(strings.ToLower(file.Name), ".mp4") {
			task.Files = append(task.Files, file)
		}
	}
	if len(task.Files) == 0 {
		s.update(task, "no_mp4", "下载完成，未找到 MP4")
		return
	}
	s.update(task, "ready", "下载完成")
}
