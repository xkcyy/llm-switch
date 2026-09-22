package proxy

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"llm-switch/internal/config"
	"llm-switch/internal/proxy/protocol"
)

var (
	ErrMissingModel     = errors.New("缺少模型名称")
	ErrProviderNotFound = errors.New("供应商不可用")
	ErrModelNotFound    = errors.New("模型不可用")
	ErrAmbiguous        = errors.New("模型名称歧义")
)

// Match 是一次成功的模型映射结果。
type Match struct {
	Provider config.Provider
	Model    config.Model
}

// UpstreamProtocol 选择本次请求使用的上游协议：
// 优先与入口协议一致（直通，保真度最高），否则按供应商配置顺序选择第一个可转换的协议。
// 模型级 protocol 固定时只使用该协议（多端点网关的个别模型可能需要固定）。
func (m Match) UpstreamProtocol(entry protocol.Protocol) (protocol.Protocol, error) {
	candidates := m.Provider.UpstreamProtocols()
	if pin := strings.TrimSpace(m.Model.Protocol); pin != "" {
		candidates = []string{pin}
	}
	for _, c := range candidates {
		if p, err := protocol.Parse(c); err == nil && p == entry {
			return p, nil
		}
	}
	for _, c := range candidates {
		p, err := protocol.Parse(c)
		if err != nil {
			continue
		}
		if protocol.CanConvert(entry, p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("协议不兼容：入口 %s，上游可用协议 %s", entry, strings.Join(candidates, "、"))
}

// Index 是根据配置构建的只读映射索引（仅包含已启用的供应商与模型）。
// 供应商以 ID 作为定位项；名称作为兼容别名参与匹配。
type Index struct {
	byID      map[string]config.Provider
	byName    map[string]config.Provider
	byIDLower map[string]config.Provider
	byNameLow map[string]config.Provider
	models    map[string]map[string]config.Model // 供应商 ID → 模型 ID → 模型
	plain     map[string][]Match                 // 模型 ID → 多个候选（无前缀匹配）
}

func BuildIndex(cfg *config.Config) *Index {
	ix := &Index{
		byID:      map[string]config.Provider{},
		byName:    map[string]config.Provider{},
		byIDLower: map[string]config.Provider{},
		byNameLow: map[string]config.Provider{},
		models:    map[string]map[string]config.Model{},
		plain:     map[string][]Match{},
	}
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		ix.byID[p.ID] = p
		if _, exists := ix.byIDLower[strings.ToLower(p.ID)]; !exists {
			ix.byIDLower[strings.ToLower(p.ID)] = p
		}
		if _, exists := ix.byName[p.Name]; !exists {
			ix.byName[p.Name] = p
		}
		if _, exists := ix.byNameLow[strings.ToLower(p.Name)]; !exists {
			ix.byNameLow[strings.ToLower(p.Name)] = p
		}
		ix.models[p.ID] = map[string]config.Model{}
	}
	for _, m := range cfg.Models {
		if !m.Enabled {
			continue
		}
		p, ok := ix.byID[m.ProviderID]
		if !ok {
			continue
		}
		ix.models[p.ID][m.ID] = m
		ix.plain[m.ID] = append(ix.plain[m.ID], Match{Provider: p, Model: m})
	}
	return ix
}

// Resolve 解析请求中的 model 值：供应商 ID/模型 ID（兼容供应商名称前缀）。
func (ix *Index) Resolve(raw string) (Match, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return Match{}, ErrMissingModel
	}
	if i := strings.Index(name, "/"); i >= 0 {
		providerKey, modelKey := name[:i], name[i+1:]
		p, ok := ix.lookupProvider(providerKey)
		if !ok {
			return Match{}, fmt.Errorf("%w: 供应商 %q 不存在或已停用", ErrProviderNotFound, providerKey)
		}
		if m, ok := ix.models[p.ID][modelKey]; ok {
			return Match{Provider: p, Model: m}, nil
		}
		return Match{}, fmt.Errorf("%w: %s/%s 不存在或已停用", ErrModelNotFound, providerKey, modelKey)
	}
	list := ix.plain[name]
	switch len(list) {
	case 0:
		return Match{}, fmt.Errorf("%w: %q 不存在或已停用", ErrModelNotFound, name)
	case 1:
		return list[0], nil
	default:
		names := make([]string, 0, len(list))
		for _, m := range list {
			names = append(names, m.Provider.ID)
		}
		return Match{}, fmt.Errorf("%w: %q 命中多个供应商（%s），请使用 供应商ID/模型ID", ErrAmbiguous, name, strings.Join(names, "、"))
	}
}

// lookupProvider 优先按 ID 匹配，其次按名称别名；两者命中不同供应商时报歧义。
// 均支持大小写不敏感的兜底匹配。
func (ix *Index) lookupProvider(key string) (config.Provider, bool) {
	byID, hasID := ix.byID[key]
	if !hasID {
		byID, hasID = ix.byIDLower[strings.ToLower(key)]
	}
	byName, hasName := ix.byName[key]
	if !hasName {
		byName, hasName = ix.byNameLow[strings.ToLower(key)]
	}
	switch {
	case hasID && hasName && byID.ID != byName.ID:
		return config.Provider{}, false
	case hasID:
		return byID, true
	case hasName:
		return byName, true
	}
	return config.Provider{}, false
}

// Holder 提供索引的原子替换。
type Holder struct {
	v atomic.Pointer[Index]
}

func (h *Holder) Store(ix *Index) { h.v.Store(ix) }

func (h *Holder) Load() *Index {
	if ix := h.v.Load(); ix != nil {
		return ix
	}
	return BuildIndex(&config.Config{})
}
