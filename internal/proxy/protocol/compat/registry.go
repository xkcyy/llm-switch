package compat

import "fmt"

// registry 是扩展目录。具体扩展在各自文件的 init 里自注册，
// 机制侧只认识 Extension 接口，不认识任何具体扩展。
var registry []Extension

// Register 注册一条扩展。名字重复是编程错误，直接 panic（启动即暴露）。
func Register(e Extension) {
	if e == nil {
		panic("compat: 注册了 nil 扩展")
	}
	name := e.Name()
	if name == "" {
		panic("compat: 扩展缺少名字")
	}
	if _, ok := byName[name]; ok {
		panic(fmt.Sprintf("compat: 扩展名 %q 重复注册", name))
	}
	byName[name] = e
	registry = append(registry, e)
}

var byName = map[string]Extension{}

// All 返回全部已注册扩展，顺序为注册顺序（同一包内按文件名稳定）。
func All() []Extension { return append([]Extension(nil), registry...) }

// ByName 按名字查扩展。
func ByName(name string) (Extension, bool) {
	e, ok := byName[name]
	return e, ok
}

// Names 列出全部扩展名，供界面与配置校验使用。
func Names() []string {
	out := make([]string, 0, len(registry))
	for _, e := range registry {
		out = append(out, e.Name())
	}
	return out
}
