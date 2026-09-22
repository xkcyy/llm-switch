package web

import "embed"

// FS 内嵌 Web 管理界面构建产物（由 web 目录下的 Vite 工程生成到 dist）。
//
//go:embed all:dist
var FS embed.FS
