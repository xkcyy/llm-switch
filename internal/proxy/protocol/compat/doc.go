// Package compat 是供应商兼容扩展的机制与目录。
//
// 目录结构（机制与实例分开放）：
//   - compat.go：机制——上下文、扩展接口、可选能力接口、Plan 聚合。
//   - registry.go：注册表——Register / All / ByName。
//   - ext_*.go：具体扩展实例，一个文件一个扩展，在 init 里自注册。
//
// 边界（见 CONTEXT.md）：
//   - 协议自身固有的行为（Anthropic 的 anthropic-version、产品自标识 User-Agent）
//     属于协议实现，不在这里；
//   - 只有供应商特有的怪癖才进扩展。
//
// 新增一条扩展：新建 ext_<名字>.go，实现 Extension 与需要的能力接口，在 init 里 Register。
// 扩展对象自己决定在哪个时机做什么；机制侧只按接口类型查找，不认识具体扩展。
package compat
