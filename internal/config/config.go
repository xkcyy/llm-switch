package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config 是 LLM Switch 的单一配置源（config.json）。
type Config struct {
	SchemaVersion int        `json:"schema_version"`
	Settings      Settings   `json:"settings"`
	Providers     []Provider `json:"providers"`
	Models        []Model    `json:"models"`
}

type Settings struct {
	Proxy              Endpoint    `json:"proxy"`
	Admin              Endpoint    `json:"admin"`
	Log                LogSettings `json:"log"`
	OpenBrowserOnStart bool        `json:"open_browser_on_start"`
	AutoStart          bool        `json:"auto_start"`
	DefaultModel       string      `json:"default_model"`
	CodexAutoSync      bool        `json:"codex_auto_sync"`
	OpenCodeAutoSync   bool        `json:"opencode_auto_sync"`
	OpenCodeShape      string      `json:"opencode_shape"` // auto | v1 | v2
}

type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type LogSettings struct {
	Level         string `json:"level"`
	RetentionDays int    `json:"retention_days"`
}

// Provider 是一个上游模型服务连接。Name 全局唯一且不含 "/"。
type Provider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Preset   string `json:"preset,omitempty"`
	BaseURL  string `json:"base_url"`
	Protocol string `json:"protocol,omitempty"` // 兼容字段：恒等于 Protocols[0]
	// Protocols 是该供应商上游支持的协议，按优先级排列（可多选）。
	// 入口协议命中列表时直接直通，未命中时按顺序选择第一个可转换的协议。
	Protocols []string `json:"protocols,omitempty"`
	// Extensions 是显式挂载的兼容扩展名（见 internal/proxy/protocol/compat）。
	// 自动匹配判不出来的供应商怪癖用这里兜底；未知名字会被忽略并告警。
	Extensions []string `json:"extensions,omitempty"`
	Auth       Auth     `json:"auth"`
	Enabled    bool     `json:"enabled"`
}

// SupportedProtocols 是所有合法协议。
var SupportedProtocols = []string{"chat", "responses", "messages"}

func ValidProtocol(s string) bool {
	for _, p := range SupportedProtocols {
		if s == p {
			return true
		}
	}
	return false
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// UpstreamProtocols 返回供应商可用的上游协议（按优先级），永不为空。
func (p Provider) UpstreamProtocols() []string {
	if len(p.Protocols) > 0 {
		out := make([]string, 0, len(p.Protocols))
		for _, s := range p.Protocols {
			if ValidProtocol(s) {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if ValidProtocol(p.Protocol) {
		return []string{p.Protocol}
	}
	return []string{"chat"}
}

type Auth struct {
	Type   string `json:"type"`              // bearer | api_key_header | custom
	APIKey string `json:"api_key,omitempty"` // 明文保存（需求允许），界面默认脱敏
	Header string `json:"header,omitempty"`  // api_key_header/custom 使用的请求头名
}

// Model 是供应商提供的一个模型。ID 为上游模型标识，在供应商内唯一。
type Model struct {
	ID              string     `json:"id"`
	ProviderID      string     `json:"provider_id"`
	Name            string     `json:"name,omitempty"`
	Description     string     `json:"description,omitempty"`
	Capabilities    []string   `json:"capabilities,omitempty"`
	Protocol        string     `json:"protocol,omitempty"` // 留空表示跟随供应商
	ContextWindow   *int       `json:"context_window,omitempty"`
	MaxOutputTokens *int       `json:"max_output_tokens,omitempty"`
	Reasoning       *Reasoning `json:"reasoning,omitempty"`
	Enabled         bool       `json:"enabled"`
}

type Reasoning struct {
	DefaultLevel string   `json:"default_level,omitempty"`
	Levels       []string `json:"levels,omitempty"`
}

// Repair 描述一次配置自愈。
type Repair struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Sanitize 修复配置中的不一致数据（自愈），保证单条坏数据不会拖垮整个系统。
// 返回本次修复项；无问题时返回空列表。
func (s *Store) Sanitize() ([]Repair, error) {
	var repairs []Repair
	err := s.Update(func(c *Config) error {
		repairs = sanitize(c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.repairs = repairs
	s.mu.Unlock()
	return repairs, nil
}

// LastRepairs 返回最近一次配置自愈的修复项，供界面提示。
func (s *Store) LastRepairs() []Repair {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Repair, len(s.repairs))
	copy(out, s.repairs)
	return out
}

func sanitize(c *Config) []Repair {
	var repairs []Repair
	add := func(kind, detail string) { repairs = append(repairs, Repair{Kind: kind, Detail: detail}) }

	// 供应商：补齐 ID、去重、纠正协议
	seenID := map[string]bool{}
	seenName := map[string]bool{}
	providers := c.Providers[:0]
	for _, p := range c.Providers {
		oldID := p.ID
		p.ID = NormalizeProviderID(p.ID)
		switch {
		case p.ID == "":
			p.ID = NormalizeProviderID(p.Name)
			if p.ID == "" {
				p.ID = fmt.Sprintf("provider-%d", len(providers)+1)
			}
			add("provider.id", fmt.Sprintf("供应商 %q 缺少 ID，已自动设为 %q", p.Name, p.ID))
		case p.ID != oldID:
			add("provider.id", fmt.Sprintf("供应商 ID %q 已归一化为 %q", oldID, p.ID))
		}
		if oldID != "" && oldID != p.ID {
			// ID 变更必须级联，否则下面的孤儿清理会把该供应商的模型删掉
			c.cascadeProviderID(oldID, p.ID)
		}
		switch p.Protocol {
		case "chat", "responses", "messages":
		default:
			add("provider.protocol", fmt.Sprintf("供应商 %q 的协议 %q 无效，已回退为 chat", p.ID, p.Protocol))
			p.Protocol = "chat"
		}
		// 协议列表：去重、校验、补默认；兼容旧配置（只有 protocol 单值）。
		var protocols []string
		for _, raw := range p.Protocols {
			proto := strings.ToLower(strings.TrimSpace(raw))
			if !ValidProtocol(proto) {
				add("provider.protocol", fmt.Sprintf("供应商 %q 的协议 %q 无效，已忽略", p.ID, raw))
				continue
			}
			if containsString(protocols, proto) {
				continue
			}
			protocols = append(protocols, proto)
		}
		if len(protocols) == 0 {
			protocols = []string{p.Protocol}
		}
		p.Protocols = protocols
		p.Protocol = protocols[0]
		// 兼容扩展名：去空、去重；不认识的名字保留（可能是更新版本才支持的扩展）。
		var extensions []string
		for _, raw := range p.Extensions {
			name := strings.TrimSpace(raw)
			if name == "" || containsString(extensions, name) {
				continue
			}
			extensions = append(extensions, name)
		}
		p.Extensions = extensions
		if seenID[strings.ToLower(p.ID)] {
			add("provider.duplicate", fmt.Sprintf("供应商 ID %q 重复，已忽略后一个", p.ID))
			continue
		}
		if p.Name != "" && seenName[strings.ToLower(p.Name)] {
			add("provider.duplicate", fmt.Sprintf("供应商名称 %q 重复，已忽略后一个", p.Name))
			continue
		}
		seenID[strings.ToLower(p.ID)] = true
		if p.Name != "" {
			seenName[strings.ToLower(p.Name)] = true
		}
		providers = append(providers, p)
	}
	c.Providers = providers

	// 模型：供应商必须存在；同一供应商内 ID 唯一
	validProvider := map[string]bool{}
	for _, p := range c.Providers {
		validProvider[p.ID] = true
	}
	seenModel := map[string]bool{}
	models := c.Models[:0]
	for _, m := range c.Models {
		if !validProvider[m.ProviderID] {
			add("model.orphan", fmt.Sprintf("模型 %q 指向不存在的供应商 %q，已移除", m.ID, m.ProviderID))
			continue
		}
		key := ModelSlug(m.ProviderID, m.ID)
		if seenModel[key] {
			add("model.duplicate", fmt.Sprintf("模型 %q 在同一供应商内重复，已忽略后一个", key))
			continue
		}
		if m.Protocol != "" && !ValidProtocol(m.Protocol) {
			add("model.protocol", fmt.Sprintf("模型 %q 的协议 %q 无效，已改为跟随供应商", key, m.Protocol))
			m.Protocol = ""
		}
		seenModel[key] = true
		models = append(models, m)
	}
	c.Models = models

	// 默认模型：必须在可用模型里；否则回退到第一个可用模型
	list := c.AvailableModels()
	available := make(map[string]bool, len(list))
	for _, a := range list {
		available[a.Slug] = true
	}
	fallback := c.defaultModelFallback()
	if c.Settings.DefaultModel != "" && !available[c.Settings.DefaultModel] {
		old := c.Settings.DefaultModel
		c.Settings.DefaultModel = fallback
		if fallback == "" {
			add("settings.default_model", fmt.Sprintf("默认模型 %q 已失效且当前没有可用模型，已清空", old))
		} else {
			add("settings.default_model", fmt.Sprintf("默认模型 %q 已失效，已自动改为 %q", old, fallback))
		}
	}
	if c.Settings.DefaultModel == "" && fallback != "" {
		c.Settings.DefaultModel = fallback
		add("settings.default_model", fmt.Sprintf("未设置默认模型，已自动设为 %q", fallback))
	}
	return repairs
}

// Dir 返回配置目录（当前用户目录）。
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".llm-switch"
	}
	return filepath.Join(home, ".llm-switch")
}

func DefaultPath() string { return filepath.Join(Dir(), "config.json") }

func Default() Config {
	return Config{
		SchemaVersion: 1,
		Settings: Settings{
			Proxy:              Endpoint{Host: "127.0.0.1", Port: 8317},
			Admin:              Endpoint{Host: "127.0.0.1", Port: 8318},
			Log:                LogSettings{Level: "info", RetentionDays: 7},
			OpenBrowserOnStart: true,
			CodexAutoSync:      true,
			OpenCodeAutoSync:   true,
			OpenCodeShape:      "auto",
		},
		Providers: []Provider{},
		Models:    []Model{},
	}
}

// Store 提供带原子写入和变更通知的配置访问。
type Store struct {
	mu      sync.RWMutex
	path    string
	cfg     Config
	subs    []func()
	repairs []Repair
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.cfg = Default()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := s.save(&s.cfg); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
		}
		// 新增的接入开关：老配置里没有该字段时按推荐值（开启）处理；
		// 用户显式写入的 false 保持不变。
		var probe struct {
			Settings map[string]json.RawMessage `json:"settings"`
		}
		if json.Unmarshal(b, &probe) == nil {
			if _, ok := probe.Settings["opencode_auto_sync"]; !ok {
				s.cfg.Settings.OpenCodeAutoSync = true
			}
		}
		s.cfg.normalize()
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out Config
	b, _ := json.Marshal(s.cfg)
	_ = json.Unmarshal(b, &out)
	return out
}

// Update 在写锁内修改配置副本并原子落盘，成功后在锁外通知订阅者。
// 任何校验失败都不会污染内存中的现有配置。
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	next := s.cfg.clone()
	if err := fn(&next); err != nil {
		s.mu.Unlock()
		return err
	}
	next.normalize()
	if err := s.save(&next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cfg = next
	subs := append([]func(){}, s.subs...)
	s.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
	return nil
}

func (c Config) clone() Config {
	var out Config
	b, _ := json.Marshal(c)
	_ = json.Unmarshal(b, &out)
	return out
}

func (c *Config) normalize() {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = 1
	}
	def := Default()
	if c.Settings.Proxy.Host == "" {
		c.Settings.Proxy.Host = def.Settings.Proxy.Host
	}
	if c.Settings.Proxy.Port == 0 {
		c.Settings.Proxy.Port = def.Settings.Proxy.Port
	}
	if c.Settings.Admin.Host == "" {
		c.Settings.Admin.Host = def.Settings.Admin.Host
	}
	if c.Settings.Admin.Port == 0 {
		c.Settings.Admin.Port = def.Settings.Admin.Port
	}
	if c.Settings.Log.Level == "" {
		c.Settings.Log.Level = def.Settings.Log.Level
	}
	if c.Settings.Log.RetentionDays <= 0 {
		c.Settings.Log.RetentionDays = def.Settings.Log.RetentionDays
	}
	switch strings.ToLower(strings.TrimSpace(c.Settings.OpenCodeShape)) {
	case "v1", "v2":
		c.Settings.OpenCodeShape = strings.ToLower(strings.TrimSpace(c.Settings.OpenCodeShape))
	default:
		c.Settings.OpenCodeShape = "auto"
	}
	if c.Providers == nil {
		c.Providers = []Provider{}
	}
	if c.Models == nil {
		c.Models = []Model{}
	}
}

func (s *Store) Subscribe(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
}

func (s *Store) save(c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// FindProvider 返回供应商副本。
func (c *Config) FindProvider(id string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

func (c *Config) FindProviderByName(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

func (c *Config) FindModel(providerID, modelID string) (Model, bool) {
	for _, m := range c.Models {
		if m.ProviderID == providerID && m.ID == modelID {
			return m, true
		}
	}
	return Model{}, false
}

func (c *Config) ModelsOf(providerID string) []Model {
	out := []Model{}
	for _, m := range c.Models {
		if m.ProviderID == providerID {
			out = append(out, m)
		}
	}
	return out
}

// ModelSlug 返回模型的对外请求名：供应商ID/模型ID。
// 这是代理侧的定位契约（请求路由、Codex 模型目录、OpenCode 模型键、Trae 显示名共用），
// 所有对外暴露模型名的地方都必须走它，避免格式各处重写。
func ModelSlug(providerID, modelID string) string {
	return providerID + "/" + modelID
}

// AvailableModel 是一个可用模型：已启用的供应商 + 已启用的模型。
// Slug 是对外请求名，Provider / Model 是完整定义，调用方无需再回查。
type AvailableModel struct {
	Slug     string
	Provider Provider
	Model    Model
}

// availableModel 判定单个模型是否可用，并给出可用形态。
// 「可用」的唯一定义在这里：供应商存在且已启用、模型已启用。
func (c *Config) availableModel(m Model) (AvailableModel, bool) {
	if !m.Enabled {
		return AvailableModel{}, false
	}
	p, ok := c.FindProvider(m.ProviderID)
	if !ok || !p.Enabled {
		return AvailableModel{}, false
	}
	return AvailableModel{Slug: ModelSlug(p.ID, m.ID), Provider: p, Model: m}, true
}

// AvailableModels 返回全部可用模型，按 Models 的配置顺序。
// 代理对外暴露的模型清单（/v1/models、Codex 目录、OpenCode 配置、Trae 接入、
// 默认模型校验）一律走这里，不再各自按启用状态重算。
func (c *Config) AvailableModels() []AvailableModel {
	out := make([]AvailableModel, 0, len(c.Models))
	for _, m := range c.Models {
		if a, ok := c.availableModel(m); ok {
			out = append(out, a)
		}
	}
	return out
}

// LookupAvailable 按对外请求名（供应商ID/模型ID）取一个可用模型。
func (c *Config) LookupAvailable(slug string) (AvailableModel, bool) {
	if slug == "" {
		return AvailableModel{}, false
	}
	for _, m := range c.Models {
		if a, ok := c.availableModel(m); ok && a.Slug == slug {
			return a, true
		}
	}
	return AvailableModel{}, false
}

// cascadeProviderID 把模型归属与默认模型里的旧供应商 ID 换成新 ID。
// 供应商 ID 一旦变更（写入或启动自愈），必须走这里，否则模型会变成孤儿被清理掉。
func (c *Config) cascadeProviderID(oldID, newID string) {
	if oldID == "" || oldID == newID {
		return
	}
	for i := range c.Models {
		if c.Models[i].ProviderID == oldID {
			c.Models[i].ProviderID = newID
		}
	}
	if strings.HasPrefix(c.Settings.DefaultModel, oldID+"/") {
		c.Settings.DefaultModel = ModelSlug(newID, strings.TrimPrefix(c.Settings.DefaultModel, oldID+"/"))
	}
}

// defaultModelFallback 返回默认模型失效时该改成的值：第一个可用模型；没有可用模型时为空串。
func (c *Config) defaultModelFallback() string {
	if list := c.AvailableModels(); len(list) > 0 {
		return list[0].Slug
	}
	return ""
}

// ensureDefaultModel 维持「默认模型必须可用」这条不变量：
// 已设置但已失效时回退到第一个可用模型，没有可用模型时清空。
// 本来就是空的不自动补——那是启动自愈的职责（它要记录修复项）。
func (c *Config) ensureDefaultModel() {
	if c.Settings.DefaultModel == "" {
		return
	}
	if _, ok := c.LookupAvailable(c.Settings.DefaultModel); ok {
		return
	}
	c.Settings.DefaultModel = c.defaultModelFallback()
}

// ---- 供应商 / 模型的写入 ----
//
// 这里集中「写入新数据时要维持的不变量」：ID 归一化、唯一性、协议归一化、
// 改 ID 的级联、删除供应商的级联。调用方（HTTP 适配器、托盘、将来的 CLI）
// 只负责把输入转成 Draft 并处理错误，不再各自实现这些规则。
//
// 与 sanitize 的分工：sanitize 是「读入已存在的数据时尽力修复」，姿态宽容
// （丢弃非法项并记录）；本节的写入接口姿态严格（非法即报错、整体不落盘）。
// 两者规则相同、姿态不同，因此各自实现，不强行合并。

// NormalizeProviderID 是供应商 ID 的唯一归一化规则，用户填写的 ID 与由名称推导的 ID 共用它：
// 去首尾空白 → 空格转连字符 → 只保留 ASCII 字母数字、- _ . 与汉字 → 转小写。
// 供应商 ID 会成为对外请求名的前缀（供应商ID/模型ID），因此这里保证它不含空白与斜杠。
// 名称里没有任何可用字符时返回空串，由调用方兜底（随机 ID 或 provider-N）。
func NormalizeProviderID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, " ", "-")
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case isCJK(r):
			b.WriteRune(r)
		}
	}
	return strings.ToLower(b.String())
}

// isCJK 判断是否汉字（含扩展 A），用于全中文名称的 ID 推导。
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

// ProviderDraft 是一次供应商写入的输入。
// 零值语义：Enabled 为 nil 时新建取 true、更新保持原值；Extensions 为 nil 时新建为空、更新保持原值。
type ProviderDraft struct {
	ID         string
	Name       string
	Preset     string
	BaseURL    string
	Protocol   string // 兼容旧字段（单值）
	Protocols  []string
	Extensions *[]string
	Auth       AuthDraft
	Enabled    *bool
}

// AuthDraft 是认证输入：Type 留空按 bearer；api_key_header/custom 且 Header 留空时回退 x-api-key；
// APIKey 留空在更新时表示保留原 Key。
type AuthDraft struct {
	Type   string
	APIKey string
	Header string
}

func (d ProviderDraft) auth() Auth {
	a := Auth{Type: d.Auth.Type, APIKey: d.Auth.APIKey, Header: d.Auth.Header}
	if a.Type == "" {
		a.Type = "bearer"
	}
	if a.Header == "" && (a.Type == "api_key_header" || a.Type == "custom") {
		a.Header = "x-api-key"
	}
	return a
}

// protocols 归一化协议列表：兼容旧的单值字段，去重并校验。
func (d ProviderDraft) protocols() ([]string, error) {
	raw := d.Protocols
	if len(raw) == 0 && strings.TrimSpace(d.Protocol) != "" {
		raw = []string{d.Protocol}
	}
	out := []string{}
	for _, r := range raw {
		proto := strings.ToLower(strings.TrimSpace(r))
		if proto == "" {
			continue
		}
		if !ValidProtocol(proto) {
			return nil, fmt.Errorf("协议 %q 无效，必须是 chat、responses 或 messages", r)
		}
		if !containsString(out, proto) {
			out = append(out, proto)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("请至少选择一个协议")
	}
	return out, nil
}

func (d ProviderDraft) validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("供应商名称不能为空")
	}
	if strings.TrimSpace(d.BaseURL) == "" || !strings.HasPrefix(d.BaseURL, "http") {
		return errors.New("接口地址必须以 http(s):// 开头")
	}
	if _, err := d.protocols(); err != nil {
		return err
	}
	switch d.Auth.Type {
	case "", "bearer", "api_key_header", "custom":
	default:
		return errors.New("认证类型不支持")
	}
	return nil
}

// AddProvider 新增供应商：归一化 ID、校验输入、检查名称与 ID 唯一，然后追加。
func (c *Config) AddProvider(d ProviderDraft) (Provider, error) {
	if err := d.validate(); err != nil {
		return Provider{}, err
	}
	if _, ok := c.FindProviderByName(d.Name); ok {
		return Provider{}, fmt.Errorf("供应商名称 %q 已存在", d.Name)
	}
	id := NormalizeProviderID(d.ID)
	if id == "" {
		id = NormalizeProviderID(d.Name)
	}
	if id == "" {
		id = randomProviderID()
	}
	for _, p := range c.Providers {
		if strings.EqualFold(p.ID, id) {
			return Provider{}, fmt.Errorf("供应商 ID %q 已存在，请换一个", id)
		}
	}
	protocols, err := d.protocols()
	if err != nil {
		return Provider{}, err
	}
	enabled := true
	if d.Enabled != nil {
		enabled = *d.Enabled
	}
	p := Provider{
		ID: id, Name: d.Name, Preset: d.Preset, BaseURL: strings.TrimRight(d.BaseURL, "/"),
		Protocol: protocols[0], Protocols: protocols, Auth: d.auth(), Enabled: enabled,
	}
	if d.Extensions != nil {
		p.Extensions = *d.Extensions
	}
	c.Providers = append(c.Providers, p)
	return p, nil
}

// UpdateProvider 更新供应商；改 ID 时级联更新模型归属与默认模型。
func (c *Config) UpdateProvider(id string, d ProviderDraft) (Provider, error) {
	if err := d.validate(); err != nil {
		return Provider{}, err
	}
	protocols, err := d.protocols()
	if err != nil {
		return Provider{}, err
	}
	for i := range c.Providers {
		if c.Providers[i].ID != id {
			continue
		}
		for _, other := range c.Providers {
			if other.ID != id && other.Name == d.Name {
				return Provider{}, fmt.Errorf("供应商名称 %q 已存在", d.Name)
			}
		}
		p := &c.Providers[i]
		oldID := p.ID
		newID := NormalizeProviderID(d.ID)
		if newID == "" {
			newID = oldID
		}
		if !strings.EqualFold(newID, oldID) {
			for _, other := range c.Providers {
				if strings.EqualFold(other.ID, newID) {
					return Provider{}, fmt.Errorf("供应商 ID %q 已存在，请换一个", newID)
				}
			}
		}
		p.ID = newID
		p.Name, p.Preset = d.Name, d.Preset
		p.Protocols, p.Protocol = protocols, protocols[0]
		// 缺省表示保留原值：界面未提交该字段时不覆盖
		if d.Extensions != nil {
			p.Extensions = *d.Extensions
		}
		p.BaseURL = strings.TrimRight(d.BaseURL, "/")
		auth := d.auth()
		if auth.APIKey == "" {
			auth.APIKey = p.Auth.APIKey // 留空表示保留原 Key
		}
		p.Auth = auth
		if d.Enabled != nil {
			p.Enabled = *d.Enabled
		}
		if newID != oldID {
			c.cascadeProviderID(oldID, newID)
		}
		// 停用供应商会让它的模型从可用模型里消失，默认模型随之可能失效
		c.ensureDefaultModel()
		return *p, nil
	}
	return Provider{}, errors.New("供应商不存在")
}

// RemoveProvider 删除供应商及其全部模型，返回被连带删除的模型数量。
func (c *Config) RemoveProvider(id string) (int, error) {
	idx := -1
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, errors.New("供应商不存在")
	}
	c.Providers = append(c.Providers[:idx], c.Providers[idx+1:]...)
	removed := 0
	models := c.Models[:0]
	for _, m := range c.Models {
		if m.ProviderID == id {
			removed++
			continue
		}
		models = append(models, m)
	}
	c.Models = models
	// 被删掉的供应商可能正是默认模型的来源
	c.ensureDefaultModel()
	return removed, nil
}

// ModelDraft 是一次模型写入的输入。Name 留空表示与 ID 一致；Enabled 为 nil 时新建取 true、更新保持原值。
type ModelDraft struct {
	ID              string
	Name            string
	Description     string
	Capabilities    []string
	Protocol        string // 留空表示跟随供应商
	ContextWindow   *int
	MaxOutputTokens *int
	Reasoning       *Reasoning
	Enabled         *bool
}

func (d ModelDraft) validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("模型 ID 不能为空")
	}
	// 上游模型 ID 允许包含 /（例如 Qwen/Qwen3.6-35B-A3B），对外请求名会自动拼接供应商 ID 前缀
	switch d.Protocol {
	case "", "chat", "responses", "messages":
	default:
		return errors.New("模型协议必须是 chat、responses、messages 或留空（跟随供应商）")
	}
	if d.Reasoning != nil && d.Reasoning.DefaultLevel != "" && len(d.Reasoning.Levels) > 0 {
		found := false
		for _, l := range d.Reasoning.Levels {
			if strings.EqualFold(l, d.Reasoning.DefaultLevel) {
				found = true
			}
		}
		if !found {
			return errors.New("默认推理档位不在支持列表中")
		}
	}
	return nil
}

// AddModel 在指定供应商下新增模型；首次添加时自动设为默认模型。
func (c *Config) AddModel(providerID string, d ModelDraft) (Model, error) {
	if err := d.validate(); err != nil {
		return Model{}, err
	}
	p, ok := c.FindProvider(providerID)
	if !ok {
		return Model{}, errors.New("供应商不存在")
	}
	if _, ok := c.FindModel(providerID, d.ID); ok {
		return Model{}, fmt.Errorf("模型 %q 已存在", d.ID)
	}
	name := d.Name
	if name == "" {
		name = d.ID
	}
	enabled := true
	if d.Enabled != nil {
		enabled = *d.Enabled
	}
	m := Model{
		ID: d.ID, ProviderID: providerID, Name: name, Description: d.Description,
		Capabilities: d.Capabilities, Protocol: d.Protocol, ContextWindow: d.ContextWindow,
		MaxOutputTokens: d.MaxOutputTokens, Reasoning: d.Reasoning, Enabled: enabled,
	}
	c.Models = append(c.Models, m)
	// 首次添加模型时自动设为默认模型，用户无需任何操作
	if c.Settings.DefaultModel == "" {
		c.Settings.DefaultModel = ModelSlug(p.ID, d.ID)
	}
	return m, nil
}

// UpdateModel 更新模型；改 ID 时检查同供应商内不重复。
func (c *Config) UpdateModel(providerID, modelID string, d ModelDraft) (Model, error) {
	if err := d.validate(); err != nil {
		return Model{}, err
	}
	for i := range c.Models {
		m := &c.Models[i]
		if m.ProviderID != providerID || m.ID != modelID {
			continue
		}
		if d.ID != modelID {
			if _, exists := c.FindModel(providerID, d.ID); exists {
				return Model{}, fmt.Errorf("模型 %q 已存在", d.ID)
			}
			m.ID = d.ID
		}
		m.Name, m.Description = d.Name, d.Description
		m.Capabilities, m.Protocol = d.Capabilities, d.Protocol
		m.ContextWindow = d.ContextWindow
		m.MaxOutputTokens, m.Reasoning = d.MaxOutputTokens, d.Reasoning
		if d.Enabled != nil {
			m.Enabled = *d.Enabled
		}
		if m.Name == "" {
			m.Name = m.ID
		}
		// 停用默认模型会让它从可用模型里消失
		c.ensureDefaultModel()
		return *m, nil
	}
	return Model{}, errors.New("模型不存在")
}

// RemoveModel 删除一个模型。
func (c *Config) RemoveModel(providerID, modelID string) error {
	idx := -1
	for i := range c.Models {
		if c.Models[i].ProviderID == providerID && c.Models[i].ID == modelID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("模型不存在")
	}
	c.Models = append(c.Models[:idx], c.Models[idx+1:]...)
	// 删掉的可能正是默认模型
	c.ensureDefaultModel()
	return nil
}

// randomProviderID 在名称推导不出可用 ID 时兜底（例如名称全是符号）。
func randomProviderID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "provider"
	}
	return "prv_" + hex.EncodeToString(b)
}
