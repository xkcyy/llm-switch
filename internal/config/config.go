package config

import (
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
	Auth      Auth     `json:"auth"`
	Enabled   bool     `json:"enabled"`
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

// DeriveProviderID 由名称推导一个可用的供应商 ID：优先英文，全中文时保留中文。
func DeriveProviderID(name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	base = strings.ReplaceAll(base, " ", "-")
	base = strings.ReplaceAll(base, "_", "-")
	var ascii, cjk strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			ascii.WriteRune(r)
		case r > 127:
			cjk.WriteRune(r)
		}
	}
	out := strings.Trim(ascii.String(), "-")
	if out == "" {
		out = strings.Trim(cjk.String(), "-")
	}
	return out
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
		if strings.TrimSpace(p.ID) == "" {
			p.ID = DeriveProviderID(p.Name)
			if p.ID == "" {
				p.ID = fmt.Sprintf("provider-%d", len(providers)+1)
			}
			add("provider.id", fmt.Sprintf("供应商 %q 缺少 ID，已自动设为 %q", p.Name, p.ID))
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
	enabledProvider := map[string]bool{}
	for _, p := range c.Providers {
		validProvider[p.ID] = true
		if p.Enabled {
			enabledProvider[p.ID] = true
		}
	}
	seenModel := map[string]bool{}
	models := c.Models[:0]
	for _, m := range c.Models {
		if !validProvider[m.ProviderID] {
			add("model.orphan", fmt.Sprintf("模型 %q 指向不存在的供应商 %q，已移除", m.ID, m.ProviderID))
			continue
		}
		key := m.ProviderID + "/" + m.ID
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
	available := map[string]bool{}
	firstAvailable := ""
	for _, m := range c.Models {
		if !m.Enabled || !enabledProvider[m.ProviderID] {
			continue
		}
		slug := m.ProviderID + "/" + m.ID
		available[slug] = true
		if firstAvailable == "" {
			firstAvailable = slug
		}
	}
	if c.Settings.DefaultModel != "" && !available[c.Settings.DefaultModel] {
		old := c.Settings.DefaultModel
		c.Settings.DefaultModel = firstAvailable
		if firstAvailable == "" {
			add("settings.default_model", fmt.Sprintf("默认模型 %q 已失效且当前没有可用模型，已清空", old))
		} else {
			add("settings.default_model", fmt.Sprintf("默认模型 %q 已失效，已自动改为 %q", old, firstAvailable))
		}
	}
	if c.Settings.DefaultModel == "" && firstAvailable != "" {
		c.Settings.DefaultModel = firstAvailable
		add("settings.default_model", fmt.Sprintf("未设置默认模型，已自动设为 %q", firstAvailable))
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
