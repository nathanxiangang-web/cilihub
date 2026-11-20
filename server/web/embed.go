// Package web 暴露构建脚本写入的前端产物（dist/index.html + dist/assets）。
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:dist
var FS embed.FS

// Dist 返回以 dist 为根的只读文件系统；index.html 缺失时返回错误，让进程在启动期失败。
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(FS, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("dist/index.html 缺失: %w", err)
	}
	return sub, nil
}