// Package web 内嵌本地模式的前端静态资源。
//
// 云端由 nginx 独立托管前端（Go 不 serve dist/）；本地模式没有 nginx，
// 由这里把 front-end/dist 编进二进制后直接发。构建前把产物拷进 dist/，
// 见 Makefile 的 embed-frontend。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS 返回内嵌的前端资源根（对应 front-end/dist）。
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
