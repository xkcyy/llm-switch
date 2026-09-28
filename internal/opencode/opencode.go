// Package opencode 负责 OpenCode 配置的生成与写入（<配置目录>/opencode.json）。
//
// 只管理 llm-switch 这一个 provider 段：文件里的其它内容（用户自己的 provider、
// model、plugins、disabled_providers 等）全部原样保留。写入采用「校验 → 临时文件 → 原子替换」。
//
// OpenCode 1.x 与 2.x 的 provider 形状不同，本包按文件现状自动选择（也可在设置里强制）：
//   - V1（1.x，2.x 亦兼容读取）：provider.<id> = { name, npm, models, options:{ baseURL, apiKey } }
//   - V2（2.x 原生）：providers.<id> = { name, package:"aisdk:…", settings:{ baseURL, apiKey }, models }
package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"llm-switch/internal/config"
)

const (
	// ProviderID 是写入 OpenCode 配置的供应商标识，模型请求名为 llm-switch/<模型键>。
	ProviderID = "llm-switch"
	// ProviderName 展示名。
	ProviderName = "LLM Switch"
	// placeholderKey 是本机代理的占位密钥：代理不校验，真实密钥保存在 LLM Switch 中。
	placeholderKey = "llm-switch-local"

	npmPackageV1 = "@ai-sdk/openai-compatible"       // OpenCode 1.x 的 npm 字段
	packageV2    = "aisdk:@ai-sdk/openai-compatible" // OpenCode 2.x 的 package 字段
)

// 配置形状。auto 表示按现有文件自动判断。
const (
	ShapeAuto = "auto"
	ShapeV1   = "v1"
	ShapeV2   = "v2"
)

var (
	// ErrNoModels 表示当前没有可用模型，无法生成配置。
	ErrNoModels = errors.New("没有可用的模型，无法生成 OpenCode 配置")
	// ErrJSONCOnly 表示只找到 opencode.jsonc（可能含注释），为避免丢注释而拒绝改写。
	ErrJSONCOnly = errors.New("检测到 opencode.jsonc（可能含注释）：为避免丢失注释，LLM Switch 不会改写它。请在同一目录创建 opencode.json（内容可以是 {}）后重试")
)

// Paths 描述 OpenCode 的全局配置文件位置。
type Paths struct {
	Dir      string // 配置目录
	Config   string // 写入目标：opencode.json
	JSONC    string // 同目录的 opencode.jsonc（用于提示）
	Explicit bool   // 由 OPENCODE_CONFIG 环境变量指定，不再考虑同目录其它文件
}

func DefaultPaths() Paths {
	if v := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); v != "" {
		return Paths{Dir: filepath.Dir(v), Config: v, Explicit: true}
	}
	var dir string
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		dir = filepath.Join(x, "opencode")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		dir = filepath.Join(home, ".config", "opencode")
	} else {
		dir = filepath.Join(".config", "opencode")
	}
	return Paths{
		Dir:    dir,
		Config: filepath.Join(dir, "opencode.json"),
		JSONC:  filepath.Join(dir, "opencode.jsonc"),
	}
}

// Overrides 是手动模式下的用户输入；自动模式全部留空。
type Overrides struct {
	BaseURL string `json:"base_url"`
	// UseDefaultModel 控制根级 model 字段：
	//   nil（未传）→ 不动；true → 写成本机代理的默认模型；false → 移除本插件写入的默认模型。
	UseDefaultModel *bool  `json:"use_default_model"`
	ConfigJSON      string `json:"config_json"` // 高级：手动编辑的整份配置内容
}

// Plan 是一次同步的完整计划，手动与自动共用同一套生成逻辑。
type Plan struct {
	Path            string
	BaseURL         string
	Shape           string   // v1 | v2
	Model           string   // 将写入根级 model 的值（空表示不写）
	RemoveRootModel bool     // 需要删除根级 model（此前指向本插件）
	ConfigJSON      string   // 最终文件内容
	Models          []string // 写入的模型键（供应商ID/模型ID）
	Warnings        []string
}

type FileInfo struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
}

type Status struct {
	Connected       bool                `json:"connected"`
	AutoSync        bool                `json:"auto_sync"`
	Shape           string              `json:"shape"`
	ShapeSetting    string              `json:"shape_setting"`
	BaseURL         string              `json:"base_url"`
	RootModel       string              `json:"root_model"`
	UseDefaultModel bool                `json:"use_default_model"`
	DefaultModel    string              `json:"default_model"`
	ConfigPath      string              `json:"config_path"`
	Models          []string            `json:"models"`
	OtherProviders  []string            `json:"other_providers"`
	LastSync        string              `json:"last_sync,omitempty"`
	Files           map[string]FileInfo `json:"files"`
	Generated       string              `json:"generated"`
	Warnings        []string            `json:"warnings,omitempty"`
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

// BaseURL 返回 OpenCode 应指向的本机代理地址（OpenAI 兼容入口）。
func (s *Service) BaseURL(cfg config.Config) string {
	return fmt.Sprintf("http://%s:%d/v1", cfg.Settings.Proxy.Host, cfg.Settings.Proxy.Port)
}

// resolveShape 决定本次写入用哪种形状：
// 手动指定优先；否则以"用户自己的 provider 写在哪种形状里"为准（避免把我们自己的形状当成用户的形状）；
// 用户两种都没有时，跟随本插件已有条目；最后回退 V1（2.x 也能读，兼容性最好）。
func resolveShape(setting string, doc map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case ShapeV1:
		return ShapeV1
	case ShapeV2:
		return ShapeV2
	}
	userV1, oursV1 := countProviders(doc, "provider")
	userV2, oursV2 := countProviders(doc, "providers")
	switch {
	case userV2 > 0 && userV1 == 0:
		return ShapeV2
	case userV1 > 0 && userV2 == 0:
		return ShapeV1
	case oursV2 > 0 && oursV1 == 0:
		return ShapeV2
	}
	return ShapeV1
}

// countProviders 统计某种形状下用户自己的 provider 数量与本插件的条目数。
func countProviders(doc map[string]any, key string) (user int, ours int) {
	root, ok := doc[key].(map[string]any)
	if !ok {
		return 0, 0
	}
	for name := range root {
		if name == ProviderID {
			ours++
		} else {
			user++
		}
	}
	return user, ours
}

// modelEntry 生成单个模型条目。
//
// limit 的规则两种形状不同（实测）：
//   - V2（2.x 原生）：context / output 都是可选的，知道哪个写哪个；
//   - V1（1.x）：只要写了 limit，output 就是必填，因此只在两者都已知时才写。
//
// 能力声明一律不写——OpenCode 对未知模型默认按"支持工具 + 文本/图片输入"处理，误写反而会削弱能力。
func modelEntry(m config.Model, slug, shape string) map[string]any {
	entry := map[string]any{"name": slug}
	if shape == ShapeV2 {
		entry["modelID"] = slug
	}
	ctx, out := m.ContextWindow, m.MaxOutputTokens
	hasCtx := ctx != nil && *ctx > 0
	hasOut := out != nil && *out > 0
	switch {
	case shape == ShapeV2 && (hasCtx || hasOut):
		limit := map[string]any{}
		if hasCtx {
			limit["context"] = *ctx
		}
		if hasOut {
			limit["output"] = *out
		}
		entry["limit"] = limit
	case hasCtx && hasOut:
		entry["limit"] = map[string]any{"context": *ctx, "output": *out}
	}
	return entry
}

// buildProvider 生成 llm-switch 段的内容与模型键列表。
func buildProvider(cfg *config.Config, baseURL, shape string) (map[string]any, []string) {
	models := map[string]any{}
	keys := []string{}
	for _, a := range cfg.AvailableModels() {
		// 模型键即请求名：供应商ID/模型ID，保证代理侧确定性匹配（同名模型不歧义）
		models[a.Slug] = modelEntry(a.Model, a.Slug, shape)
		keys = append(keys, a.Slug)
	}
	sort.Strings(keys)
	if shape == ShapeV2 {
		return map[string]any{
			"name":    ProviderName,
			"package": packageV2,
			"settings": map[string]any{
				"baseURL": baseURL,
				"apiKey":  placeholderKey,
			},
			"models": models,
		}, keys
	}
	return map[string]any{
		"name":   ProviderName,
		"npm":    npmPackageV1,
		"models": models,
		"options": map[string]any{
			"baseURL": baseURL,
			"apiKey":  placeholderKey,
		},
	}, keys
}

// resolveTarget 决定写入哪个文件，并在只存在 jsonc 时给出明确错误。
func (s *Service) resolveTarget() (string, []string, error) {
	if s.paths.Explicit {
		return s.paths.Config, nil, nil
	}
	var warnings []string
	if _, err := os.Stat(s.paths.Config); err == nil {
		if _, err := os.Stat(s.paths.JSONC); err == nil {
			warnings = append(warnings, "检测到 opencode.jsonc 与 opencode.json 同时存在：OpenCode 会合并两者，本次只更新 opencode.json")
		}
		return s.paths.Config, warnings, nil
	}
	if _, err := os.Stat(s.paths.JSONC); err == nil {
		return "", nil, ErrJSONCOnly
	}
	return s.paths.Config, nil, nil
}

func (s *Service) BuildPlan(ov Overrides) (*Plan, error) {
	cfg := s.store.Snapshot()
	target, warnings, err := s.resolveTarget()
	if err != nil {
		return nil, err
	}
	plan := &Plan{Path: target, Warnings: warnings}
	plan.BaseURL = strings.TrimSpace(ov.BaseURL)
	if plan.BaseURL == "" {
		plan.BaseURL = s.BaseURL(cfg)
	}

	// 手动编辑：整份内容直接校验后写入，形状以内容为准
	if strings.TrimSpace(ov.ConfigJSON) != "" {
		doc, err := decodeDoc([]byte(ov.ConfigJSON))
		if err != nil {
			return nil, fmt.Errorf("配置内容不是合法 JSON: %w", err)
		}
		shape := resolveShape(cfg.Settings.OpenCodeShape, doc)
		keys, err := inspectModels(doc)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			return nil, errors.New("手动编辑的内容里缺少 provider[\"llm-switch\"] 或模型列表为空")
		}
		out, err := encodeDoc(doc)
		if err != nil {
			return nil, err
		}
		plan.Shape, plan.Models, plan.ConfigJSON = shape, keys, string(out)
		if root, ok := doc["model"].(string); ok {
			plan.Model = root
		}
		return plan, nil
	}

	existing, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	doc, err := decodeDoc(existing)
	if err != nil {
		return nil, fmt.Errorf("解析现有 %s 失败: %w", filepath.Base(target), err)
	}
	shape := resolveShape(cfg.Settings.OpenCodeShape, doc)
	plan.Shape = shape
	prov, keys := buildProvider(&cfg, plan.BaseURL, shape)
	if len(keys) == 0 {
		return nil, ErrNoModels
	}
	plan.Models = keys
	if _, ok := doc["$schema"]; !ok {
		doc["$schema"] = "https://opencode.ai/config.json"
	}

	// 写入目标形状，并清理另一种形状下的同名残留，避免 OpenCode 里出现重复条目
	key := "provider"
	if shape == ShapeV2 {
		key = "providers"
	}
	other := "providers"
	if shape == ShapeV2 {
		other = "provider"
	}
	if otherMap, ok := doc[other].(map[string]any); ok {
		if _, stale := otherMap[ProviderID]; stale {
			delete(otherMap, ProviderID)
			if len(otherMap) == 0 {
				delete(doc, other)
			}
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("已移除另一形状（%s）下的同名 llm-switch 段，避免重复", other))
		}
	}
	root, _ := doc[key].(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	root[ProviderID] = prov
	doc[key] = root

	// 根级 model（OpenCode 默认模型）按用户选择处理
	rootModel, _ := doc["model"].(string)
	ours := strings.HasPrefix(rootModel, ProviderID+"/")
	switch {
	case ov.UseDefaultModel != nil && *ov.UseDefaultModel:
		def := cfg.Settings.DefaultModel
		if !contains(keys, def) {
			def = keys[0]
			if rootModel != "" && cfg.Settings.DefaultModel != "" {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("默认模型 %q 不在可用模型里，已改用 %q", cfg.Settings.DefaultModel, def))
			}
		}
		doc["model"] = config.ModelSlug(ProviderID, def)
		plan.Model = config.ModelSlug(ProviderID, def)
	case ov.UseDefaultModel != nil && !*ov.UseDefaultModel:
		if ours {
			delete(doc, "model")
			plan.RemoveRootModel = true
		}
	case ours:
		// 未指定时：此前写入的默认模型若已失效，自动回退，避免 OpenCode 起不来
		target := strings.TrimPrefix(rootModel, ProviderID+"/")
		if !contains(keys, target) {
			def := cfg.Settings.DefaultModel
			if !contains(keys, def) {
				def = keys[0]
			}
			doc["model"] = config.ModelSlug(ProviderID, def)
			plan.Model = config.ModelSlug(ProviderID, def)
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("OpenCode 默认模型 %q 已失效，已自动改用 %q", rootModel, plan.Model))
		}
	}

	out, err := encodeDoc(doc)
	if err != nil {
		return nil, err
	}
	plan.ConfigJSON = string(out)
	return plan, nil
}

// Apply 校验并原子写入配置；失败不会破坏原文件。
func (s *Service) Apply(plan *Plan) error {
	doc, err := decodeDoc([]byte(plan.ConfigJSON))
	if err != nil {
		return fmt.Errorf("OpenCode 配置校验失败: %w", err)
	}
	keys, err := inspectModels(doc)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("OpenCode 配置校验失败：缺少 llm-switch provider 或模型列表为空")
	}
	if err := writeFileAtomic(plan.Path, []byte(plan.ConfigJSON), 0o644); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastSync = time.Now()
	s.mu.Unlock()
	return nil
}

// Connected 表示配置里已经存在本插件写入的 provider（任一形状）。
func (s *Service) Connected() bool {
	target, _, err := s.resolveTarget()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return false
	}
	doc, err := decodeDoc(b)
	if err != nil {
		return false
	}
	keys, err := inspectModels(doc)
	return err == nil && len(keys) > 0
}

// Disconnect 只移除本插件写入的 provider 段与默认模型，其它内容保持不变。
func (s *Service) Disconnect() error {
	target, _, err := s.resolveTarget()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	doc, err := decodeDoc(b)
	if err != nil {
		return fmt.Errorf("解析现有 %s 失败: %w", filepath.Base(target), err)
	}
	for _, key := range []string{"provider", "providers"} {
		root, ok := doc[key].(map[string]any)
		if !ok {
			continue
		}
		delete(root, ProviderID)
		if len(root) == 0 {
			delete(doc, key)
		}
	}
	if root, ok := doc["model"].(string); ok && strings.HasPrefix(root, ProviderID+"/") {
		delete(doc, "model")
	}
	out, err := encodeDoc(doc)
	if err != nil {
		return err
	}
	return writeFileAtomic(target, out, 0o644)
}

func (s *Service) Status() (*Status, error) {
	cfg := s.store.Snapshot()
	target, warnings, resolveErr := s.resolveTarget()
	setting := strings.ToLower(strings.TrimSpace(cfg.Settings.OpenCodeShape))
	if setting == "" {
		setting = ShapeAuto
	}
	st := &Status{
		BaseURL:        s.BaseURL(cfg),
		AutoSync:       cfg.Settings.OpenCodeAutoSync,
		ShapeSetting:   setting,
		DefaultModel:   cfg.Settings.DefaultModel,
		ConfigPath:     target,
		Files:          map[string]FileInfo{},
		Models:         []string{},
		OtherProviders: []string{},
	}
	if st.ConfigPath == "" {
		st.ConfigPath = s.paths.Config
	}
	s.mu.Lock()
	if !s.lastSync.IsZero() {
		st.LastSync = s.lastSync.Format(time.RFC3339)
	}
	s.mu.Unlock()
	st.Warnings = append(st.Warnings, warnings...)

	b, err := os.ReadFile(st.ConfigPath)
	switch {
	case err == nil:
		st.Files["config"] = FileInfo{Path: st.ConfigPath, Exists: true, Content: string(b)}
		doc, derr := decodeDoc(b)
		if derr != nil {
			st.Warnings = append(st.Warnings, "现有 opencode.json 不是合法 JSON："+derr.Error())
			break
		}
		st.Shape = resolveShape(cfg.Settings.OpenCodeShape, doc)
		if root, ok := doc["model"].(string); ok {
			st.RootModel = root
			st.UseDefaultModel = strings.HasPrefix(root, ProviderID+"/")
		}
		// 其它 provider 一并展示（两种形状都看）
		for _, key := range []string{"provider", "providers"} {
			root, ok := doc[key].(map[string]any)
			if !ok {
				continue
			}
			for name := range root {
				if name != ProviderID {
					st.OtherProviders = append(st.OtherProviders, name)
				}
			}
		}
		sort.Strings(st.OtherProviders)
		keys, _ := inspectModels(doc)
		st.Models = keys
		st.Connected = len(keys) > 0
	case errors.Is(err, os.ErrNotExist):
		st.Files["config"] = FileInfo{Path: st.ConfigPath, Exists: false}
		st.Shape = resolveShape(cfg.Settings.OpenCodeShape, map[string]any{})
	default:
		st.Warnings = append(st.Warnings, "读取配置失败："+err.Error())
	}

	if resolveErr != nil {
		st.Warnings = append(st.Warnings, resolveErr.Error())
		return st, nil
	}
	plan, perr := s.BuildPlan(Overrides{})
	if perr != nil {
		if !errors.Is(perr, ErrNoModels) {
			st.Warnings = append(st.Warnings, "生成 OpenCode 配置时出现问题："+perr.Error())
		}
		return st, nil
	}
	if st.Shape == "" {
		st.Shape = plan.Shape
	}
	if plan.Shape != st.Shape {
		st.Warnings = append(st.Warnings, fmt.Sprintf("本次将写入 %s 形状，与当前文件里的 %s 形状不一致，可能造成重复条目", plan.Shape, st.Shape))
	}
	st.Warnings = append(st.Warnings, plan.Warnings...)
	st.Generated = plan.ConfigJSON
	return st, nil
}

// ---- 工具 ----

func decodeDoc(b []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return doc, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // 保留数字原样，避免大整数被转成科学计数法
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func encodeDoc(doc map[string]any) ([]byte, error) {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// inspectModels 返回配置里本插件的模型键（两种形状都识别，合并去重）。
func inspectModels(doc map[string]any) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, key := range []string{"provider", "providers"} {
		root, ok := doc[key].(map[string]any)
		if !ok {
			continue
		}
		item, ok := root[ProviderID].(map[string]any)
		if !ok {
			continue
		}
		models, ok := item["models"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s.%s 缺少 models 对象", key, ProviderID)
		}
		for k := range models {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
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
