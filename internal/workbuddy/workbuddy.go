// Package workbuddy 负责 WorkBuddy（腾讯 CodeBuddy 系桌面端）自定义模型配置的生成与写入。
//
// WorkBuddy 的「设置 → 模型 → 添加模型 → 自定义 API」把每条自定义模型写在用户目录：
//
//	~/.workbuddy/models.json
//
// 文件既可以是 JSON 数组，也可以是 { "models": [...] } 形式；WorkBuddy 自身会监听该
// 文件的变化，改完下一次请求即生效，无需重启。本包只增删「由本工具写入」的条目
// （带 llmSwitchManaged 标记，或指向本机代理端口），其它自定义模型原样保留。
//
// 自定义模型条目只走 OpenAI 兼容的 Chat Completions：url 指向本机代理的
// /v1/chat/completions，useCustomProtocol=true 表示「地址原样使用、不再自动拼接」。
package workbuddy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"llm-switch/internal/config"
)

const (
	// Filename 是 WorkBuddy 保存自定义模型的文件名。
	Filename = "models.json"
	// VendorName 是写入条目的 vendor 展示名。
	VendorName = "LLM Switch"

	// markerKey 是写入条目的所有权标记。WorkBuddy 只校验已知字段的类型，
	// 额外字段会被原样保留，因此可以安全地用它区分「本工具写入」与用户自建条目。
	markerKey = "llmSwitchManaged"

	// placeholderKey 是本机代理的占位密钥：代理不校验，真实密钥保存在 LLM Switch 中。
	placeholderKey = "llm-switch-local"
)

// ErrNoModels 表示当前没有可用模型，无法生成配置。
var ErrNoModels = errors.New("没有可用的模型，无法生成 WorkBuddy 配置")

// configDirEnv 是 WorkBuddy/CodeBuddy 允许覆盖配置目录的环境变量。
var configDirEnv = []string{"WORKBUDDY_CONFIG_DIR", "CODEBUDDY_CONFIG_DIR"}

// reasoningEfforts 是 WorkBuddy 模型选择器支持的原生推理档位。
var reasoningEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

// ConfigDir 返回 WorkBuddy 的用户配置目录。
func ConfigDir() string {
	for _, name := range configDirEnv {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".workbuddy")
	}
	return ".workbuddy"
}

// ModelsPath 返回自定义模型文件路径。
func ModelsPath() string {
	return filepath.Join(ConfigDir(), Filename)
}

// Installed 判断本机是否安装 / 运行过 WorkBuddy（只看常见痕迹，不保证正在运行）。
func Installed() bool {
	if _, err := os.Stat(ConfigDir()); err == nil {
		return true
	}
	for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA")} {
		if base == "" {
			continue
		}
		for _, name := range []string{"WorkBuddy", "WorkBuddy AI", `Programs\WorkBuddy`, `Programs\WorkBuddy AI`} {
			if _, err := os.Stat(filepath.Join(base, name)); err == nil {
				return true
			}
		}
	}
	return false
}

// Overrides 是一次同步的用户输入；留空表示使用设置里的推荐值。
type Overrides struct {
	BaseURL string `json:"base_url"`
}

// Plan 是一次同步的完整计划。
type Plan struct {
	Path     string   `json:"path"`
	BaseURL  string   `json:"base_url"`
	ChatURL  string   `json:"chat_url"`
	Models   []string `json:"models"`
	Content  string   `json:"content"`
	Warnings []string `json:"warnings,omitempty"`
}

// FileInfo 描述被读取的配置文件。
type FileInfo struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
}

// Status 是接入页所需的完整状态。
type Status struct {
	Connected   bool     `json:"connected"`
	AutoSync    bool     `json:"auto_sync"`
	Installed   bool     `json:"installed"`
	BaseURL     string   `json:"base_url"`
	ChatURL     string   `json:"chat_url"`
	ConfigPath  string   `json:"config_path"`
	Format      string   `json:"format"`
	Models      []string `json:"models"`
	OtherModels []string `json:"other_models"`
	File        FileInfo `json:"file"`
	Generated   string   `json:"generated"`
	Warnings    []string `json:"warnings,omitempty"`
	LastSync    string   `json:"last_sync,omitempty"`
}

type Service struct {
	store *config.Store

	mu       sync.Mutex
	lastSync time.Time
}

func NewService(store *config.Store) *Service {
	return &Service{store: store}
}

// BaseURL 返回 WorkBuddy 应指向的本机代理地址（OpenAI 兼容入口根）。
func (s *Service) BaseURL(cfg config.Config) string {
	return fmt.Sprintf("http://%s:%d/v1", cfg.Settings.Proxy.Host, cfg.Settings.Proxy.Port)
}

// BuildPlan 读取现有文件，合并 LLM Switch 的自定义模型，产出最终 JSON 内容。
func (s *Service) BuildPlan(ov Overrides) (*Plan, error) {
	cfg := s.store.Snapshot()
	baseURL := strings.TrimSpace(ov.BaseURL)
	if baseURL == "" {
		baseURL = s.BaseURL(cfg)
	}
	chatURL := strings.TrimRight(baseURL, "/") + "/chat/completions"

	path := ModelsPath()
	plan := &Plan{Path: path, BaseURL: baseURL, ChatURL: chatURL}

	models, entries, err := buildEntries(&cfg, chatURL)
	if err != nil {
		return nil, err
	}
	plan.Models = models

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	d, err := parseDoc(existing)
	if err != nil {
		return nil, fmt.Errorf("解析现有 %s 失败: %w", Filename, err)
	}

	port := cfg.Settings.Proxy.Port
	kept := make([]json.RawMessage, 0, len(d.entries)+len(entries))
	for _, raw := range d.entries {
		if isOurs(parseEntry(raw), port) {
			continue
		}
		kept = append(kept, raw)
	}
	kept = append(kept, entries...)
	plan.Content = string(encodeDocPretty(d, kept))
	return plan, nil
}

// Apply 校验并原子写入；内容与现有文件一致时不落盘。
func (s *Service) Apply(plan *Plan) error {
	d, err := parseDoc([]byte(plan.Content))
	if err != nil {
		return fmt.Errorf("WorkBuddy 配置校验失败: %w", err)
	}
	if countManaged(d) == 0 {
		return errors.New("WorkBuddy 配置校验失败：没有 LLM Switch 模型条目")
	}
	cur, err := os.ReadFile(plan.Path)
	if err == nil && bytes.Equal(cur, []byte(plan.Content)) {
		s.markSynced()
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeFileAtomic(plan.Path, []byte(plan.Content), 0o600); err != nil {
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

// Connected 表示 models.json 里已经存在本工具写入的条目。
func (s *Service) Connected() bool {
	b, err := os.ReadFile(ModelsPath())
	if err != nil {
		return false
	}
	d, err := parseDoc(b)
	if err != nil {
		return false
	}
	return countManaged(d) > 0
}

// Disconnect 只移除本工具写入的条目，其它自定义模型保持不变。
func (s *Service) Disconnect() error {
	path := ModelsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	d, err := parseDoc(b)
	if err != nil {
		return fmt.Errorf("解析现有 %s 失败: %w", Filename, err)
	}
	port := s.store.Snapshot().Settings.Proxy.Port
	kept := make([]json.RawMessage, 0, len(d.entries))
	for _, raw := range d.entries {
		if isOurs(parseEntry(raw), port) {
			continue
		}
		kept = append(kept, raw)
	}
	out := []byte(encodeDocPretty(d, kept))
	if bytes.Equal(b, out) {
		return nil
	}
	return writeFileAtomic(path, out, 0o600)
}

// Status 返回接入页状态；读取失败只记录告警，不阻断页面。
func (s *Service) Status() (*Status, error) {
	cfg := s.store.Snapshot()
	path := ModelsPath()
	st := &Status{
		AutoSync:    cfg.Settings.WorkBuddyAutoSync,
		Installed:   Installed(),
		BaseURL:     s.BaseURL(cfg),
		ChatURL:     strings.TrimRight(s.BaseURL(cfg), "/") + "/chat/completions",
		ConfigPath:  path,
		Format:      "array",
		Models:      []string{},
		OtherModels: []string{},
		File:        FileInfo{Path: path},
	}
	s.mu.Lock()
	if !s.lastSync.IsZero() {
		st.LastSync = s.lastSync.Format(time.RFC3339)
	}
	s.mu.Unlock()

	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		st.File = FileInfo{Path: path, Exists: true, Content: string(b)}
		d, derr := parseDoc(b)
		if derr != nil {
			st.Warnings = append(st.Warnings, "现有 models.json 不是合法 JSON："+derr.Error())
			break
		}
		st.Format = d.format
		ours, others := classify(d, cfg.Settings.Proxy.Port)
		st.Models = ours
		st.OtherModels = others
		st.Connected = len(ours) > 0
	case errors.Is(err, os.ErrNotExist):
		st.File = FileInfo{Path: path, Exists: false}
	default:
		st.Warnings = append(st.Warnings, "读取配置失败："+err.Error())
	}

	if !st.Installed {
		st.Warnings = append(st.Warnings, "未发现 WorkBuddy 配置目录（"+ConfigDir()+"）。请先安装并启动一次 WorkBuddy。")
	}

	plan, perr := s.BuildPlan(Overrides{})
	if perr != nil {
		if !errors.Is(perr, ErrNoModels) {
			st.Warnings = append(st.Warnings, "生成 WorkBuddy 配置时出现问题："+perr.Error())
		}
		return st, nil
	}
	st.Generated = plan.Content
	return st, nil
}

// ---- 条目构造 ----

// managedEntry 是写入 models.json 的一条自定义模型。
type managedEntry struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Vendor            string          `json:"vendor"`
	URL               string          `json:"url"`
	APIKey            string          `json:"apiKey"`
	SupportsToolCall  bool            `json:"supportsToolCall"`
	SupportsImages    bool            `json:"supportsImages"`
	SupportsReasoning bool            `json:"supportsReasoning"`
	UseCustomProtocol bool            `json:"useCustomProtocol"`
	MaxInputTokens    *int            `json:"maxInputTokens,omitempty"`
	MaxOutputTokens   *int            `json:"maxOutputTokens,omitempty"`
	Reasoning         *entryReasoning `json:"reasoning,omitempty"`
	Managed           bool            `json:"llmSwitchManaged"`
}

type entryReasoning struct {
	DefaultEffort    string   `json:"defaultEffort,omitempty"`
	SupportedEfforts []string `json:"supportedEfforts,omitempty"`
}

// buildEntries 依据可用模型生成写入条目与 slug 清单。
func buildEntries(cfg *config.Config, chatURL string) ([]string, []json.RawMessage, error) {
	slugs := []string{}
	entries := []json.RawMessage{}
	for _, a := range cfg.AvailableModels() {
		raw, err := json.Marshal(entryFor(a, chatURL))
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, raw)
		slugs = append(slugs, a.Slug)
	}
	if len(entries) == 0 {
		return nil, nil, ErrNoModels
	}
	return slugs, entries, nil
}

func entryFor(a config.AvailableModel, chatURL string) managedEntry {
	m := a.Model
	e := managedEntry{
		ID:                a.Slug,
		Name:              a.Slug,
		Vendor:            VendorName,
		URL:               chatURL,
		APIKey:            placeholderKey,
		SupportsToolCall:  supportsTools(m.Capabilities),
		SupportsImages:    hasVision(m.Capabilities),
		UseCustomProtocol: true,
		Managed:           true,
	}
	if m.ContextWindow != nil && *m.ContextWindow > 0 {
		v := *m.ContextWindow
		e.MaxInputTokens = &v
	}
	if m.MaxOutputTokens != nil && *m.MaxOutputTokens > 0 {
		v := *m.MaxOutputTokens
		e.MaxOutputTokens = &v
	}
	if efforts, def := reasoningOf(m.Reasoning); len(efforts) > 0 {
		e.SupportsReasoning = true
		e.Reasoning = &entryReasoning{DefaultEffort: def, SupportedEfforts: efforts}
	}
	return e
}

func supportsTools(caps []string) bool {
	if len(caps) == 0 {
		return true
	}
	for _, c := range caps {
		if strings.EqualFold(strings.TrimSpace(c), "tools") {
			return true
		}
	}
	return false
}

func hasVision(caps []string) bool {
	for _, c := range caps {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "vision", "image", "multimodal", "image_input":
			return true
		}
	}
	return false
}

// reasoningOf 把模型档位映射到 WorkBuddy 的原生档位集合；无有效档位时返回空。
func reasoningOf(r *config.Reasoning) ([]string, string) {
	if r == nil || len(r.Levels) == 0 {
		return nil, ""
	}
	present := map[string]bool{}
	for _, l := range r.Levels {
		present[strings.ToLower(strings.TrimSpace(l))] = true
	}
	efforts := []string{}
	for _, lvl := range reasoningEfforts {
		if present[lvl] {
			efforts = append(efforts, lvl)
		}
	}
	if len(efforts) == 0 {
		return nil, ""
	}
	def := strings.ToLower(strings.TrimSpace(r.DefaultLevel))
	if !contains(efforts, def) {
		def = efforts[len(efforts)/2]
	}
	return efforts, def
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---- JSON 文档操作 ----

// doc 是 models.json 的解析结果。wrapper 非空时为对象形式 { ..., "models": [...] }。
type doc struct {
	format  string
	wrapper *orderedObject
	entries []json.RawMessage
}

func parseDoc(b []byte) (*doc, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return &doc{format: "array", entries: []json.RawMessage{}}, nil
	}
	switch trimmed[0] {
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, err
		}
		return &doc{format: "array", entries: arr}, nil
	case '{':
		obj, err := decodeOrderedObject(trimmed)
		if err != nil {
			return nil, err
		}
		d := &doc{format: "object", wrapper: obj, entries: []json.RawMessage{}}
		if raw, ok := obj.vals["models"]; ok {
			var arr []json.RawMessage
			if err := json.Unmarshal(raw, &arr); err != nil {
				return nil, errors.New("models 字段不是数组")
			}
			d.entries = arr
		}
		return d, nil
	default:
		return nil, errors.New("顶层应为 JSON 数组或对象")
	}
}

// encodeDocPretty 重建文档并按 2 空格缩进输出；未改动的条目保持原始字段顺序。
func encodeDocPretty(d *doc, entries []json.RawMessage) []byte {
	if entries == nil {
		entries = []json.RawMessage{}
	}
	arr, _ := json.Marshal(entries)
	var compact []byte
	if d.format == "object" && d.wrapper != nil {
		compact = d.wrapper.encodeReplacing("models", arr)
	} else {
		compact = arr
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return compact
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// orderedObject 保留 JSON 对象的键顺序。
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func decodeOrderedObject(b []byte) (*orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("顶层不是 JSON 对象")
	}
	obj := &orderedObject{vals: map[string]json.RawMessage{}}
	for dec.More() {
		ktok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := ktok.(string)
		if !ok {
			return nil, errors.New("对象键不是字符串")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if _, dup := obj.vals[key]; !dup {
			obj.keys = append(obj.keys, key)
		}
		obj.vals[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return obj, nil
}

func (o *orderedObject) encodeReplacing(key string, replacement json.RawMessage) []byte {
	keys := o.keys
	if _, ok := o.vals[key]; !ok {
		keys = append(append([]string{}, keys...), key)
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		if k == key {
			buf.Write(replacement)
		} else {
			buf.Write(o.vals[k])
		}
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// entryInfo 是判定所有权与统计所需的字段。
type entryInfo struct {
	ID     string
	URL    string
	Marker bool
}

func parseEntry(raw json.RawMessage) entryInfo {
	var probe struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		URL     string `json:"url"`
		Managed bool   `json:"llmSwitchManaged"`
	}
	_ = json.Unmarshal(raw, &probe)
	id := strings.TrimSpace(probe.ID)
	if id == "" {
		id = strings.TrimSpace(probe.Name)
	}
	return entryInfo{ID: id, URL: probe.URL, Marker: probe.Managed}
}

// isOurs 判定条目是否由本工具写入：优先看标记，其次看是否指向本机代理端口。
func isOurs(info entryInfo, proxyPort int) bool {
	if info.Marker {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(info.URL))
	if err != nil {
		return false
	}
	if !isLoopback(u.Hostname()) {
		return false
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return port == strconv.Itoa(proxyPort)
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1", "0.0.0.0":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

func countManaged(d *doc) int {
	n := 0
	for _, raw := range d.entries {
		if parseEntry(raw).Marker {
			n++
		}
	}
	return n
}

// classify 返回本工具写入的模型 id 与其它自定义模型 id。
func classify(d *doc, proxyPort int) ([]string, []string) {
	ours := []string{}
	others := []string{}
	for _, raw := range d.entries {
		info := parseEntry(raw)
		if isOurs(info, proxyPort) {
			if info.ID != "" {
				ours = append(ours, info.ID)
			}
			continue
		}
		if info.ID != "" {
			others = append(others, info.ID)
		}
	}
	sort.Strings(ours)
	sort.Strings(others)
	return ours, others
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
