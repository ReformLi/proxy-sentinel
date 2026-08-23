// Package web 提供内嵌的静态前端资源。
// 前端源码位于 web/frontend（React 18 + Vite + Tailwind + ECharts），
// 构建产物输出到 web/dist 并嵌入二进制，实现单文件部署。
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// dist 返回构建产物子文件系统
func dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// Static 返回静态资源处理器（/assets/*、favicon 等）
func Static() http.Handler {
	sub, err := dist()
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

// Index 返回 SPA 入口 index.html（页面路由统一由前端接管）
func Index() ([]byte, error) {
	return distFS.ReadFile("dist/index.html")
}
