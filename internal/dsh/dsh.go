// Package dsh 负责 DeepSeek Harness（dsh）配置的生成与写入。
//
// dsh 的模型提供方配置落在当前 profile 的 Cordis patch：
//
//	$DSH_HOME/profiles/<profile>/cordis.patch.yml
//
// 桌面端 profile 为 desktop，Web 端为 web。本包只维护其中
// llm-pi-ai 条目下 providers["llm-switch"] 这一段，文件里的其它条目、
// provider、注释与 !!js 表达式全部原样保留。写入采用「校验 → 临时文件 → 原子替换」。
package dsh

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"llm-switch/internal/config"
)

const (
	// ProviderID 是写入 dsh 的 provider 路由键；模型请求名为 <模型 ID>，即 供应商ID/模型ID。
	ProviderID = "llm-switch"
	// ProviderName 展示名。
	ProviderName = "LLM Switch"

	// EntryID 是 dsh 内置 pi-ai 适配器的 profile 条目 id。
	EntryID = "llm-pi-ai"
	// EntryName 是该条目的包名。
	EntryName = "@deepseek-ai/dsh-llm-pi-ai"
	// ProtocolChat 是 pi-ai 的 OpenAI Chat Completions 协议名。
	ProtocolChat = "openai-completions"

	// PatchFilename 是 profile 用户 patch 文件名。
	PatchFilename = "cordis.patch.yml"
	// ProfileDesktop 是桌面端 profile 名。
	ProfileDesktop = "desktop"
	// ProfileWeb 是 dsh web 的 profile 名。
	ProfileWeb = "web"

	// placeholderKey 是本机代理的占位密钥：代理不校验，真实密钥保存在 LLM Switch 中。
	placeholderKey = "llm-switch-local"
)

// ErrNoModels 表示当前没有可用模型，无法生成配置。
var ErrNoModels = errors.New("没有可用的模型，无法生成 dsh 配置")

// Paths 描述一个 dsh profile 的配置位置。
type Paths struct {
	Home        string // DSH_HOME
	ProfilesDir string // <home>/profiles
	Profile     string // desktop | web | ...
	ProfileDir  string // <home>/profiles/<profile>
	Patch       string // <profileDir>/cordis.patch.yml
}

// Home 解析 dsh 主目录：DSH_HOME 优先，否则 ~/.dsh。
func Home() string {
	if v := strings.TrimSpace(os.Getenv("DSH_HOME")); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".dsh")
	}
	return ".dsh"
}

// ResolvePaths 返回指定 profile 的路径；profile 为空时回退 desktop。
func ResolvePaths(profile string) Paths {
	if strings.TrimSpace(profile) == "" {
		profile = ProfileDesktop
	}
	home := Home()
	dir := filepath.Join(home, "profiles", profile)
	return Paths{
		Home:        home,
		ProfilesDir: filepath.Join(home, "profiles"),
		Profile:     profile,
		ProfileDir:  dir,
		Patch:       filepath.Join(dir, PatchFilename),
	}
}

// AvailableProfiles 返回磁盘上已存在的 profile 名（含目录即可），按名称排序。
func AvailableProfiles() []string {
	entries, err := os.ReadDir(filepath.Join(Home(), "profiles"))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Overrides 是一次同步的用户输入；留空表示使用设置里的推荐值。
type Overrides struct {
	BaseURL string `json:"base_url"`
	Profile string `json:"profile"`
}

// Plan 是一次同步的完整计划。
type Plan struct {
	Path       string   `json:"path"`
	Profile    string   `json:"profile"`
	BaseURL    string   `json:"base_url"`
	ProviderID string   `json:"provider_id"`
	Models     []string `json:"models"`
	Content    string   `json:"content"`
	Warnings   []string `json:"warnings,omitempty"`
}

// FileInfo 描述一个被读取的配置文件。
type FileInfo struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
}

// Status 是接入页所需的完整状态。
type Status struct {
	Connected      bool                `json:"connected"`
	AutoSync       bool                `json:"auto_sync"`
	Profile        string              `json:"profile"`
	ProfileSetting string              `json:"profile_setting"`
	Profiles       []string            `json:"profiles"`
	ProfileExists  bool                `json:"profile_exists"`
	BaseURL        string              `json:"base_url"`
	ProviderID     string              `json:"provider_id"`
	ConfigPath     string              `json:"config_path"`
	Models         []string            `json:"models"`
	OtherProviders []string            `json:"other_providers"`
	Files          map[string]FileInfo `json:"files"`
	Generated      string              `json:"generated"`
	Warnings       []string            `json:"warnings,omitempty"`
	LastSync       string              `json:"last_sync,omitempty"`
}

type Service struct {
	store *config.Store

	mu       sync.Mutex
	lastSync time.Time
}

func NewService(store *config.Store) *Service {
	return &Service{store: store}
}

// BaseURL 返回 dsh 应指向的本机代理地址（OpenAI 兼容入口）。
func (s *Service) BaseURL(cfg config.Config) string {
	return fmt.Sprintf("http://%s:%d/v1", cfg.Settings.Proxy.Host, cfg.Settings.Proxy.Port)
}

func resolveProfile(override, setting string) string {
	if v := strings.TrimSpace(override); v != "" {
		return v
	}
	if v := strings.TrimSpace(setting); v != "" {
		return v
	}
	return ProfileDesktop
}

// BuildPlan 读取现有 patch，合并 llm-switch provider，产出最终 YAML 内容。
func (s *Service) BuildPlan(ov Overrides) (*Plan, error) {
	cfg := s.store.Snapshot()
	profile := resolveProfile(ov.Profile, cfg.Settings.DshProfile)
	paths := ResolvePaths(profile)
	baseURL := strings.TrimSpace(ov.BaseURL)
	if baseURL == "" {
		baseURL = s.BaseURL(cfg)
	}

	plan := &Plan{Path: paths.Patch, Profile: profile, BaseURL: baseURL, ProviderID: ProviderID}
	models, slugs, err := buildModels(&cfg)
	if err != nil {
		return nil, err
	}
	plan.Models = slugs

	existing, err := os.ReadFile(paths.Patch)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	doc, err := parsePatch(existing)
	if err != nil {
		return nil, fmt.Errorf("解析现有 %s 失败: %w", filepath.Base(paths.Patch), err)
	}
	upsertProvider(doc, baseURL, models)
	out, err := encodePatch(doc)
	if err != nil {
		return nil, err
	}
	plan.Content = string(out)
	return plan, nil
}

// Apply 校验并原子写入；内容与现有文件一致时不落盘。
func (s *Service) Apply(plan *Plan) error {
	doc, err := parsePatch([]byte(plan.Content))
	if err != nil {
		return fmt.Errorf("dsh 配置校验失败: %w", err)
	}
	if n := countModels(doc); n == 0 {
		return errors.New("dsh 配置校验失败：llm-switch provider 缺少模型列表")
	}
	cur, err := os.ReadFile(plan.Path)
	if err == nil && bytes.Equal(cur, []byte(plan.Content)) {
		s.markSynced()
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeFileAtomic(plan.Path, []byte(plan.Content), 0o644); err != nil {
		return err
	}
	s.markSynced()
	return nil
}

func (s *Service) markSynced() {
	s.mu.Lock()
	s.lastSync = time.Now()
	s.mu.Unlock()
}

// Connected 表示指定 profile 的 patch 里已经存在本插件写入的 provider。
func (s *Service) Connected(profile string) bool {
	paths := ResolvePaths(profile)
	b, err := os.ReadFile(paths.Patch)
	if err != nil {
		return false
	}
	doc, err := parsePatch(b)
	if err != nil {
		return false
	}
	return countModels(doc) > 0
}

// Disconnect 只移除本插件写入的 provider 段，其它内容保持不变。
func (s *Service) Disconnect(profile string) error {
	paths := ResolvePaths(profile)
	b, err := os.ReadFile(paths.Patch)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	doc, err := parsePatch(b)
	if err != nil {
		return fmt.Errorf("解析现有 %s 失败: %w", filepath.Base(paths.Patch), err)
	}
	removeProvider(doc)
	out, err := encodePatch(doc)
	if err != nil {
		return err
	}
	if bytes.Equal(b, out) {
		return nil
	}
	return writeFileAtomic(paths.Patch, out, 0o644)
}

// Status 返回接入页状态；读取失败只记录告警，不阻断页面。
func (s *Service) Status() (*Status, error) {
	cfg := s.store.Snapshot()
	profile := resolveProfile("", cfg.Settings.DshProfile)
	paths := ResolvePaths(profile)
	st := &Status{
		AutoSync:       cfg.Settings.DshAutoSync,
		Profile:        profile,
		ProfileSetting: cfg.Settings.DshProfile,
		Profiles:       AvailableProfiles(),
		BaseURL:        s.BaseURL(cfg),
		ProviderID:     ProviderID,
		ConfigPath:     paths.Patch,
		Models:         []string{},
		OtherProviders: []string{},
		Files:          map[string]FileInfo{},
	}
	if _, err := os.Stat(paths.ProfileDir); err == nil {
		st.ProfileExists = true
	} else if len(st.Profiles) == 0 {
		st.Warnings = append(st.Warnings, "未发现 dsh profile 目录（"+paths.ProfilesDir+"）。请先启动一次 DeepSeek Harness 桌面端。")
	}
	s.mu.Lock()
	if !s.lastSync.IsZero() {
		st.LastSync = s.lastSync.Format(time.RFC3339)
	}
	s.mu.Unlock()

	b, err := os.ReadFile(paths.Patch)
	switch {
	case err == nil:
		st.Files["patch"] = FileInfo{Path: paths.Patch, Exists: true, Content: string(b)}
		doc, derr := parsePatch(b)
		if derr != nil {
			st.Warnings = append(st.Warnings, "现有 cordis.patch.yml 不是合法 YAML 列表："+derr.Error())
			break
		}
		st.Models, st.OtherProviders = inspect(doc)
		st.Connected = len(st.Models) > 0
	case errors.Is(err, os.ErrNotExist):
		st.Files["patch"] = FileInfo{Path: paths.Patch, Exists: false}
	default:
		st.Warnings = append(st.Warnings, "读取配置失败："+err.Error())
	}

	plan, perr := s.BuildPlan(Overrides{Profile: profile})
	if perr != nil {
		if !errors.Is(perr, ErrNoModels) {
			st.Warnings = append(st.Warnings, "生成 dsh 配置时出现问题："+perr.Error())
		}
		return st, nil
	}
	st.Generated = plan.Content
	return st, nil
}

// ---- YAML 节点操作 ----

func parsePatch(b []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return emptyDoc(), nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return emptyDoc(), nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.SequenceNode {
		return nil, errors.New("patch 根节点必须是 YAML 列表")
	}
	return &doc, nil
}

func emptyDoc() *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{seq}}
}

func rootSeq(doc *yaml.Node) *yaml.Node {
	return doc.Content[0]
}

func encodePatch(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func strNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func intNode(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}
}

func nullNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
}

func mapNode(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: pairs}
}

func seqNode(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, strNode(key), val)
}

func mapDelete(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

func isMapEmpty(m *yaml.Node) bool {
	return m == nil || m.Kind != yaml.MappingNode || len(m.Content) == 0
}

// findEntry 在 patch 列表中按 id 定位条目，并递归进入 insert 子列表。
// 返回条目节点、它所在的列表节点与下标；未找到时返回 nil。
func findEntry(seq *yaml.Node, id string) (*yaml.Node, *yaml.Node, int) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil, nil, -1
	}
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if v := mapGet(item, "id"); v != nil && v.Value == id {
			return item, seq, i
		}
		if ins := mapGet(item, "insert"); ins != nil && ins.Kind == yaml.SequenceNode {
			if e, ps, pi := findEntry(ins, id); e != nil {
				return e, ps, pi
			}
		}
	}
	return nil, nil, -1
}

// upsertProvider 把 llm-switch provider 合并进 llm-pi-ai 条目，保留其它 provider 与条目。
func upsertProvider(doc *yaml.Node, baseURL string, models *yaml.Node) {
	seq := rootSeq(doc)
	entry, _, _ := findEntry(seq, EntryID)
	if entry == nil {
		entry = mapNode()
		mapSet(entry, "id", strNode(EntryID))
		mapSet(entry, "name", strNode(EntryName))
		seq.Content = append(seq.Content, entry)
	}
	cfgNode := mapGet(entry, "config")
	if cfgNode == nil || cfgNode.Kind != yaml.MappingNode {
		cfgNode = mapNode()
		mapSet(entry, "config", cfgNode)
	}
	providers := mapGet(cfgNode, "providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		providers = mapNode()
		mapSet(cfgNode, "providers", providers)
	}
	mapSet(providers, ProviderID, providerNode(baseURL, models))
}

func providerNode(baseURL string, models *yaml.Node) *yaml.Node {
	headers := mapNode(strNode("Authorization"), strNode("Bearer "+placeholderKey))
	return mapNode(
		strNode("displayName"), strNode(ProviderName),
		strNode("api"), strNode(ProtocolChat),
		strNode("baseURL"), strNode(baseURL),
		strNode("headers"), headers,
		strNode("models"), models,
	)
}

func removeProvider(doc *yaml.Node) {
	seq := rootSeq(doc)
	entry, parent, idx := findEntry(seq, EntryID)
	if entry == nil {
		return
	}
	cfgNode := mapGet(entry, "config")
	providers := mapGet(cfgNode, "providers")
	if providers == nil {
		return
	}
	mapDelete(providers, ProviderID)
	if isMapEmpty(providers) {
		mapDelete(cfgNode, "providers")
	}
	if isMapEmpty(cfgNode) {
		mapDelete(entry, "config")
	}
	// 仅剩 id / name 的条目说明是我们为接入而建的，直接移除。
	if entryRemovable(entry) {
		parent.Content = append(parent.Content[:idx], parent.Content[idx+1:]...)
	}
}

func entryRemovable(entry *yaml.Node) bool {
	for i := 0; i+1 < len(entry.Content); i += 2 {
		switch entry.Content[i].Value {
		case "id", "name":
		default:
			return false
		}
	}
	return true
}

// countModels 返回 llm-pi-ai 条目下 llm-switch provider 的模型数。
func countModels(doc *yaml.Node) int {
	seq := rootSeq(doc)
	entry, _, _ := findEntry(seq, EntryID)
	if entry == nil {
		return 0
	}
	providers := mapGet(mapGet(entry, "config"), "providers")
	prov := mapGet(providers, ProviderID)
	models := mapGet(prov, "models")
	if models == nil || models.Kind != yaml.SequenceNode {
		return 0
	}
	return len(models.Content)
}

// inspect 返回 llm-switch 的模型 id 与该条目下的其它 provider 路由键。
func inspect(doc *yaml.Node) ([]string, []string) {
	seq := rootSeq(doc)
	entry, _, _ := findEntry(seq, EntryID)
	if entry == nil {
		return []string{}, []string{}
	}
	providers := mapGet(mapGet(entry, "config"), "providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		return []string{}, []string{}
	}
	models := []string{}
	others := []string{}
	for i := 0; i+1 < len(providers.Content); i += 2 {
		name := providers.Content[i].Value
		if name != ProviderID {
			others = append(others, name)
			continue
		}
		list := mapGet(providers.Content[i+1], "models")
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		for _, m := range list.Content {
			if id := mapGet(m, "id"); id != nil && id.Value != "" {
				models = append(models, id.Value)
			}
		}
	}
	sort.Strings(models)
	sort.Strings(others)
	return models, others
}

// ---- 模型节点 ----

var reasoningLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// buildModels 依据可用模型生成 dsh 的 models 列表与模型 id 清单。
func buildModels(cfg *config.Config) (*yaml.Node, []string, error) {
	items := []*yaml.Node{}
	slugs := []string{}
	for _, a := range cfg.AvailableModels() {
		items = append(items, modelNode(a.Model, a.Slug))
		slugs = append(slugs, a.Slug)
	}
	if len(items) == 0 {
		return nil, nil, ErrNoModels
	}
	return seqNode(items...), slugs, nil
}

func modelNode(m config.Model, slug string) *yaml.Node {
	n := mapNode(
		strNode("id"), strNode(slug),
		strNode("name"), strNode(slug),
	)
	if m.ContextWindow != nil && *m.ContextWindow > 0 {
		mapSet(n, "contextWindow", intNode(*m.ContextWindow))
	}
	if m.MaxOutputTokens != nil && *m.MaxOutputTokens > 0 {
		mapSet(n, "maxTokens", intNode(*m.MaxOutputTokens))
	}
	mapSet(n, "input", inputNode(m.Capabilities))
	if e := reasoningEffortsNode(m.Reasoning); e != nil {
		mapSet(n, "reasoningEfforts", e)
	}
	return n
}

func inputNode(caps []string) *yaml.Node {
	items := []*yaml.Node{strNode("text")}
	for _, c := range caps {
		if isVisionCapability(c) {
			items = append(items, strNode("image"))
			break
		}
	}
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, Content: items}
}

func reasoningEffortsNode(r *config.Reasoning) *yaml.Node {
	if r == nil || len(r.Levels) == 0 {
		return nil
	}
	present := map[string]bool{}
	for _, l := range r.Levels {
		if v := normalizeLevel(l); v != "" {
			present[v] = true
		}
	}
	out := mapNode()
	added := false
	for _, lvl := range reasoningLevels {
		if !present[lvl] {
			continue
		}
		val := nullNode()
		if lvl != "off" {
			val = strNode(lvl)
		}
		out.Content = append(out.Content, strNode(lvl), val)
		added = true
	}
	if !added {
		return nil
	}
	return out
}

// normalizeLevel 把供应商元数据里的推理档位映射到 dsh 的档位词汇；未知档位返回空串。
func normalizeLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "off", "disable", "disabled":
		return "off"
	case "minimal", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(s))
	}
	return ""
}

func isVisionCapability(c string) bool {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "vision", "image", "multimodal", "image_input":
		return true
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
