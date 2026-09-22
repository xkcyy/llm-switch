// Package codex 负责 Codex 配置的生成与写入（config.toml、auth.json、模型目录）。
package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"llm-switch/internal/config"
)

const (
	ProviderID     = "llm-switch"
	ProviderName   = "LLM Switch"
	placeholderKey = "llm-switch-local"
)

// ErrNoModels 表示当前没有可用模型，无法生成目录。
var ErrNoModels = errors.New("没有可用的模型，无法生成模型目录")

type Paths struct {
	ConfigTOML string
	AuthJSON   string
	Catalog    string
}

func DefaultPaths() Paths {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	dir := filepath.Join(home, ".codex")
	return Paths{
		ConfigTOML: filepath.Join(dir, "config.toml"),
		AuthJSON:   filepath.Join(dir, "auth.json"),
		Catalog:    filepath.Join(dir, "llm-switch-models.json"),
	}
}

// ---- 模型目录（Codex catalog 格式） ----

type ReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type CatalogModel struct {
	Slug                       string           `json:"slug"`
	DisplayName                string           `json:"display_name"`
	Description                string           `json:"description"`
	DefaultReasoningLevel      string           `json:"default_reasoning_level"`
	SupportedReasoningLevels   []ReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                  string           `json:"shell_type"`
	Visibility                 string           `json:"visibility"`
	SupportedInAPI             bool             `json:"supported_in_api"`
	Priority                   int              `json:"priority"`
	AvailabilityNux            any              `json:"availability_nux"`
	Upgrade                    any              `json:"upgrade"`
	ModelMessages              map[string]any   `json:"model_messages"`
	SupportsReasoningSummaries bool             `json:"supports_reasoning_summaries"`
	SupportVerbosity           bool             `json:"support_verbosity"`
	DefaultVerbosity           any              `json:"default_verbosity"`
	ApplyPatchToolType         string           `json:"apply_patch_tool_type"`
	TruncationPolicy           map[string]any   `json:"truncation_policy"`
	SupportsParallelToolCalls  bool             `json:"supports_parallel_tool_calls"`
	ExperimentalSupportedTools []any            `json:"experimental_supported_tools"`
	ContextWindow              *int             `json:"context_window,omitempty"`
	InputModalities            []string         `json:"input_modalities,omitempty"`
}

type Catalog struct {
	Models []CatalogModel `json:"models"`
}

var reasoningDescriptions = map[string]string{
	"none":    "关闭推理，直接回答",
	"minimal": "最小推理",
	"low":     "轻量推理，优先速度",
	"medium":  "均衡速度与推理深度",
	"high":    "深度推理，适合复杂任务",
	"xhigh":   "超高推理强度",
	"max":     "最大推理强度",
	"maximum": "最大推理强度",
}

func BuildCatalog(cfg *config.Config) (Catalog, error) {
	cat := Catalog{Models: []CatalogModel{}}
	priority := 1
	for _, m := range cfg.Models {
		if !m.Enabled {
			continue
		}
		p, ok := cfg.FindProvider(m.ProviderID)
		if !ok || !p.Enabled {
			continue
		}
		slug := p.ID + "/" + m.ID
		// 展示名与请求名保持一致：Codex 模型列表里也显示 供应商ID/模型ID，
		// 避免同名模型来自不同供应商时无法区分。
		name := slug
		levels := []string{"low", "medium", "high"}
		defLevel := "medium"
		if m.Reasoning != nil {
			if len(m.Reasoning.Levels) > 0 {
				levels = m.Reasoning.Levels
			}
			if m.Reasoning.DefaultLevel != "" {
				defLevel = m.Reasoning.DefaultLevel
			}
		}
		supported := make([]ReasoningLevel, 0, len(levels))
		for _, l := range levels {
			desc := reasoningDescriptions[strings.ToLower(l)]
			if desc == "" {
				desc = l
			}
			supported = append(supported, ReasoningLevel{Effort: l, Description: desc})
		}
		mods := []string{"text"}
		for _, c := range m.Capabilities {
			if c == "vision" {
				mods = []string{"text", "image"}
			}
		}
		if !contains(levels, defLevel) {
			defLevel = levels[0]
		}
		cat.Models = append(cat.Models, CatalogModel{
			Slug:                       slug,
			DisplayName:                name,
			Description:                m.Description,
			DefaultReasoningLevel:      defLevel,
			SupportedReasoningLevels:   supported,
			ShellType:                  "unified_exec",
			Visibility:                 "list",
			SupportedInAPI:             true,
			Priority:                   priority,
			ModelMessages:              map[string]any{"instructions_template": "You are Codex, an AI coding agent."},
			SupportsReasoningSummaries: false,
			ApplyPatchToolType:         "freeform",
			TruncationPolicy:           map[string]any{"mode": "tokens", "limit": 10000},
			SupportsParallelToolCalls:  true,
			ExperimentalSupportedTools: []any{},
			ContextWindow:              m.ContextWindow,
			InputModalities:            mods,
		})
		priority++
	}
	return cat, nil
}

func (c Catalog) JSON() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

// Validate 校验目录是否满足 Codex 解析要求。
func (c Catalog) Validate(defaultModel string) error {
	if len(c.Models) == 0 {
		return ErrNoModels
	}
	slugs := map[string]bool{}
	for _, m := range c.Models {
		if m.Slug == "" {
			return fmt.Errorf("模型目录中存在空 slug")
		}
		if slugs[m.Slug] {
			return fmt.Errorf("模型目录中存在重复 slug: %s", m.Slug)
		}
		slugs[m.Slug] = true
		if m.DisplayName == "" || m.ShellType == "" || m.Visibility == "" || m.TruncationPolicy == nil {
			return fmt.Errorf("模型 %s 缺少必填字段", m.Slug)
		}
		if !contains(m.levels(), m.DefaultReasoningLevel) {
			return fmt.Errorf("模型 %s 的默认推理档位 %q 不在支持列表中", m.Slug, m.DefaultReasoningLevel)
		}
	}
	if defaultModel != "" && !slugs[defaultModel] {
		return fmt.Errorf("默认模型 %q 不在模型目录中", defaultModel)
	}
	return nil
}

func (m CatalogModel) levels() []string {
	out := make([]string, 0, len(m.SupportedReasoningLevels))
	for _, l := range m.SupportedReasoningLevels {
		out = append(out, l.Effort)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if strings.EqualFold(item, v) {
			return true
		}
	}
	return false
}

// ---- 同步计划与写入 ----

type Overrides struct {
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
	ConfigTOML   string `json:"config_toml"`
	AuthJSON     string `json:"auth_json"`
}

type Plan struct {
	BaseURL      string
	DefaultModel string
	CatalogPath  string
	ConfigTOML   string
	AuthJSON     string
	CatalogJSON  string
	Warnings     []string
}

type FileInfo struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
}

type Status struct {
	Connected    bool                `json:"connected"`
	AutoSync     bool                `json:"auto_sync"`
	BaseURL      string              `json:"base_url"`
	DefaultModel string              `json:"default_model"`
	CatalogPath  string              `json:"catalog_path"`
	LastSync     string              `json:"last_sync,omitempty"`
	Files        map[string]FileInfo `json:"files"`
	Generated    map[string]string   `json:"generated"`
	Warnings     []string            `json:"warnings,omitempty"`
}

type Service struct {
	store *config.Store
	paths Paths

	mu       sync.Mutex
	lastSync time.Time
}

func NewService(store *config.Store) *Service {
	return &Service{store: store, paths: DefaultPaths()}
}

func (s *Service) Paths() Paths { return s.paths }

func (s *Service) BaseURL(cfg config.Config) string {
	return fmt.Sprintf("http://%s:%d/v1", cfg.Settings.Proxy.Host, cfg.Settings.Proxy.Port)
}

// BuildPlan 生成同步计划：手动与自动共用同一套生成逻辑。
func (s *Service) BuildPlan(ov Overrides) (*Plan, error) {
	cfg := s.store.Snapshot()
	plan := &Plan{CatalogPath: s.paths.Catalog}
	plan.BaseURL = ov.BaseURL
	if plan.BaseURL == "" {
		plan.BaseURL = s.BaseURL(cfg)
	}
	plan.DefaultModel = ov.DefaultModel
	if plan.DefaultModel == "" {
		plan.DefaultModel = cfg.Settings.DefaultModel
	}
	cat, err := BuildCatalog(&cfg)
	if err != nil {
		return nil, err
	}
	if len(cat.Models) == 0 {
		return nil, ErrNoModels
	}
	// 默认模型失效时自动回退，不阻断整体流程
	slugs := map[string]bool{}
	for _, m := range cat.Models {
		slugs[m.Slug] = true
	}
	if !slugs[plan.DefaultModel] {
		fallback := cat.Models[0].Slug
		if plan.DefaultModel == "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("未设置默认模型，已自动使用 %q", fallback))
		} else {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("默认模型 %q 不在模型目录中，已自动改用 %q", plan.DefaultModel, fallback))
		}
		plan.DefaultModel = fallback
	}
	if err := cat.Validate(plan.DefaultModel); err != nil {
		return nil, err
	}
	if b, err := cat.JSON(); err == nil {
		plan.CatalogJSON = string(b)
	} else {
		return nil, err
	}
	if ov.ConfigTOML != "" {
		if err := validateTOML(ov.ConfigTOML); err != nil {
			return nil, fmt.Errorf("config.toml 语法错误: %w", err)
		}
		plan.ConfigTOML = ov.ConfigTOML
	} else {
		existing, _ := os.ReadFile(s.paths.ConfigTOML)
		merged, err := mergeConfigTOML(existing, plan.BaseURL, plan.DefaultModel, s.paths.Catalog)
		if err != nil {
			return nil, err
		}
		plan.ConfigTOML = string(merged)
	}
	if ov.AuthJSON != "" {
		if !json.Valid([]byte(ov.AuthJSON)) {
			return nil, fmt.Errorf("auth.json 不是合法 JSON")
		}
		plan.AuthJSON = ov.AuthJSON
	} else {
		plan.AuthJSON = s.defaultAuthJSON()
	}
	return plan, nil
}

// Apply 校验并原子写入全部文件；任一步失败都不会破坏原文件。
func (s *Service) Apply(plan *Plan) error {
	if err := validateTOML(plan.ConfigTOML); err != nil {
		return fmt.Errorf("config.toml 校验失败: %w", err)
	}
	if !json.Valid([]byte(plan.AuthJSON)) {
		return fmt.Errorf("auth.json 校验失败")
	}
	if !json.Valid([]byte(plan.CatalogJSON)) {
		return fmt.Errorf("模型目录校验失败")
	}
	dir := filepath.Dir(s.paths.ConfigTOML)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(s.paths.Catalog, []byte(plan.CatalogJSON), 0o644); err != nil {
		return fmt.Errorf("写入模型目录失败: %w", err)
	}
	if err := writeFileAtomic(s.paths.ConfigTOML, []byte(plan.ConfigTOML), 0o644); err != nil {
		return fmt.Errorf("写入 config.toml 失败: %w", err)
	}
	if err := writeFileAtomic(s.paths.AuthJSON, []byte(plan.AuthJSON), 0o600); err != nil {
		return fmt.Errorf("写入 auth.json 失败: %w", err)
	}
	s.mu.Lock()
	s.lastSync = time.Now()
	s.mu.Unlock()
	return nil
}

// Connected 表示 Codex 配置中是否已包含 LLM Switch 供应商。
func (s *Service) Connected() bool {
	b, err := os.ReadFile(s.paths.ConfigTOML)
	if err != nil {
		return false
	}
	var root map[string]any
	if err := toml.Unmarshal(b, &root); err != nil {
		return false
	}
	providers, ok := root["model_providers"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = providers[ProviderID]
	return ok
}

// Disconnect 只移除 LLM Switch 写入的配置，保留用户其他设置。
func (s *Service) Disconnect() error {
	b, err := os.ReadFile(s.paths.ConfigTOML)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var root map[string]any
	if err := toml.Unmarshal(b, &root); err != nil {
		return fmt.Errorf("解析现有 config.toml 失败: %w", err)
	}
	if providers, ok := root["model_providers"].(map[string]any); ok {
		delete(providers, ProviderID)
		if len(providers) == 0 {
			delete(root, "model_providers")
		}
	}
	if root["model_provider"] == ProviderID {
		delete(root, "model_provider")
	}
	if p, ok := root["model_catalog_json"].(string); ok && filepath.Clean(p) == filepath.Clean(s.paths.Catalog) {
		delete(root, "model_catalog_json")
	}
	out, err := toml.Marshal(root)
	if err != nil {
		return err
	}
	if err := validateTOML(string(out)); err != nil {
		return err
	}
	return writeFileAtomic(s.paths.ConfigTOML, out, 0o644)
}

// Status 返回 Codex 页面所需的当前状态。
func (s *Service) Status() (*Status, error) {
	cfg := s.store.Snapshot()
	st := &Status{
		Connected:   s.Connected(),
		AutoSync:    cfg.Settings.CodexAutoSync,
		BaseURL:     s.BaseURL(cfg),
		CatalogPath: s.paths.Catalog,
		Files:       map[string]FileInfo{},
		Generated:   map[string]string{},
	}
	s.mu.Lock()
	if !s.lastSync.IsZero() {
		st.LastSync = s.lastSync.Format(time.RFC3339)
	}
	s.mu.Unlock()
	for key, path := range map[string]string{"config_toml": s.paths.ConfigTOML, "auth_json": s.paths.AuthJSON} {
		fi := FileInfo{Path: path}
		if b, err := os.ReadFile(path); err == nil {
			fi.Exists = true
			fi.Content = string(b)
		}
		st.Files[key] = fi
	}
	plan, err := s.BuildPlan(Overrides{})
	if err != nil {
		if !errors.Is(err, ErrNoModels) {
			st.Warnings = append(st.Warnings, "生成 Codex 配置时出现问题："+err.Error())
		}
		return st, nil // 单点数据问题不阻断页面加载
	}
	st.DefaultModel = plan.DefaultModel
	st.Warnings = append(st.Warnings, plan.Warnings...)
	st.Generated = map[string]string{
		"config_toml": plan.ConfigTOML,
		"auth_json":   plan.AuthJSON,
		"catalog":     plan.CatalogJSON,
	}
	return st, nil
}

func (s *Service) defaultAuthJSON() string {
	if b, err := os.ReadFile(s.paths.AuthJSON); err == nil {
		var parsed map[string]any
		if json.Unmarshal(b, &parsed) == nil {
			if v, ok := parsed["OPENAI_API_KEY"].(string); ok && strings.TrimSpace(v) != "" {
				return string(b)
			}
		}
	}
	out, _ := json.MarshalIndent(map[string]string{"OPENAI_API_KEY": placeholderKey}, "", "  ")
	return string(out)
}

func validateTOML(content string) error {
	var v map[string]any
	return toml.Unmarshal([]byte(content), &v)
}

// mergeConfigTOML 合并托管键，保留用户已有配置。
func mergeConfigTOML(existing []byte, baseURL, defaultModel, catalogPath string) ([]byte, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := toml.Unmarshal(existing, &root); err != nil {
			return nil, fmt.Errorf("解析现有 config.toml 失败: %w", err)
		}
	}
	root["model_provider"] = ProviderID
	root["model"] = defaultModel
	root["model_catalog_json"] = catalogPath
	root["disable_response_storage"] = true
	providers, ok := root["model_providers"].(map[string]any)
	if !ok {
		providers = map[string]any{}
	}
	providers[ProviderID] = map[string]any{
		"name":                 ProviderName,
		"base_url":             baseURL,
		"wire_api":             "responses",
		"requires_openai_auth": true,
	}
	root["model_providers"] = providers
	out, err := toml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("生成 config.toml 失败: %w", err)
	}
	if err := validateTOML(string(out)); err != nil {
		return nil, fmt.Errorf("生成的 config.toml 无法被解析: %w", err)
	}
	return out, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".llm-switch.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
