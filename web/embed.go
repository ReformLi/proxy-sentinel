// Package web 提供内嵌的静态前端资源。
// 当前为最小可用 UI（登录页 + 仪表盘 + 日志查询 + 数据流向），
// 后续可被 web/dist 下的 React 构建产物替换。
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

// Static 返回静态资源的 http.Handler（子路径挂载到根）
func Static() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

// File 读取指定静态文件内容（用于直接返回页面 HTML）
func File(name string) ([]byte, error) {
	return staticFS.ReadFile("static/" + name)
}
