// Package template 通过 go:embed 打包前端静态资源。
package template

import "embed"

//go:embed index.html static
var FS embed.FS
