// Package buildinfo 保存构建期信息，供 HTTP 请求标识使用。
package buildinfo

// Version 由构建时注入（-X main.version）。
var Version = "dev"

// UserAgent 是 LLM Switch 对上游的自我标识。
// OpenCode Go 等上游要求客户端使用自己的 UA，而不是通用 SDK/HTTP 库名。
func UserAgent() string {
	if Version == "" || Version == "dev" {
		return "llm-switch/dev"
	}
	return "llm-switch/" + Version
}
