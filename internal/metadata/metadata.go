// Package metadata 提供模型元数据的在线补全（上下文窗口、推理档位）。
// 数据来源为公开的 models.dev 数据库，本地缓存，失败时静默降级。
package metadata

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sourceURL = "https://models.dev/api.json"
	ttl       = 7 * 24 * time.Hour
)

// presetNamespace 把供应商预置类型映射到 models.dev 的 provider 命名空间。
var presetNamespace = map[string]string{
	"opencode-go": "opencode-go",
	"deepseek":    "deepseek",
	"openai":      "openai",
	"anthropic":   "anthropic",
}

type ModelMeta struct {
	ContextWindow *int     `json:"context_window,omitempty"`
	OutputTokens  *int     `json:"output_tokens,omitempty"`
	Levels        []string `json:"levels,omitempty"`
	Description   string   `json:"description,omitempty"`
}

type cacheFile struct {
	FetchedAt time.Time                       `json:"fetched_at"`
	Providers map[string]map[string]ModelMeta `json:"providers"`
}

type Client struct {
	dir        string
	http       *http.Client
	mu         sync.Mutex
	loaded     bool
	byKey      map[string]ModelMeta // "namespace/model" 与 "*/model"
	namespaces map[string]bool
}

func New(dir string) *Client {
	return &Client{
		dir:  dir,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) cachePath() string { return filepath.Join(c.dir, "modelsdev.json") }

// Lookup 按供应商预置类型与模型 ID 查询元数据；未命中返回 nil。
// 命名空间命中优先；命名空间存在但模型未收录时不猜测（避免给错默认值）。
func (c *Client) Lookup(preset, modelID string) *ModelMeta {
	if modelID == "" {
		return nil
	}
	c.ensure()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byKey == nil {
		return nil
	}
	ns := presetNamespace[preset]
	if ns != "" && c.namespaces[ns] {
		if m, ok := c.byKey[ns+"/"+strings.ToLower(modelID)]; ok {
			return &m
		}
		if m, ok := c.byKey[ns+"/"+normalize(modelID)]; ok {
			return &m
		}
		return nil
	}
	// 未知命名空间（自定义供应商）：退化为全局精确匹配
	if m, ok := c.byKey["*/"+strings.ToLower(modelID)]; ok {
		return &m
	}
	if m, ok := c.byKey["*/"+normalize(modelID)]; ok {
		return &m
	}
	return nil
}

func normalize(id string) string {
	id = strings.ToLower(id)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	// 去掉类似 -2026-09-10 的日期后缀
	parts := strings.Split(id, "-")
	for len(parts) > 2 {
		last := parts[len(parts)-1]
		if len(last) == 8 || len(last) == 6 {
			if _, err := time.Parse("20060102", last); err == nil {
				parts = parts[:len(parts)-1]
				continue
			}
		}
		break
	}
	return strings.Join(parts, "-")
}

func (c *Client) ensure() {
	c.mu.Lock()
	if c.loaded {
		c.mu.Unlock()
		return
	}
	c.loaded = true
	c.mu.Unlock()

	if data, err := os.ReadFile(c.cachePath()); err == nil {
		var cf cacheFile
		if json.Unmarshal(data, &cf) == nil {
			c.mu.Lock()
			c.build(cf)
			c.mu.Unlock()
			if time.Since(cf.FetchedAt) < ttl {
				return
			}
		}
	}
	c.refresh()
}

func (c *Client) refresh() {
	resp, err := c.http.Get(sourceURL)
	if err != nil {
		slog.Debug("模型元数据获取失败，使用缓存或默认值", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return
	}
	var raw map[string]struct {
		Models map[string]struct {
			Description      string `json:"description"`
			ReasoningOptions []struct {
				Type   string   `json:"type"`
				Values []string `json:"values"`
			} `json:"reasoning_options"`
			Limit struct {
				Context *int `json:"context"`
				Output  *int `json:"output"`
			} `json:"limit"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		slog.Debug("模型元数据解析失败", "error", err)
		return
	}
	cf := cacheFile{FetchedAt: time.Now(), Providers: map[string]map[string]ModelMeta{}}
	for ns, provider := range raw {
		models := map[string]ModelMeta{}
		for id, m := range provider.Models {
			meta := ModelMeta{Description: m.Description, ContextWindow: m.Limit.Context, OutputTokens: m.Limit.Output}
			for _, opt := range m.ReasoningOptions {
				if opt.Type == "effort" && len(opt.Values) > 0 {
					meta.Levels = opt.Values
				}
			}
			models[id] = meta
		}
		cf.Providers[ns] = models
	}
	c.mu.Lock()
	c.build(cf)
	c.mu.Unlock()

	if err := os.MkdirAll(c.dir, 0o755); err == nil {
		if b, err := json.Marshal(cf); err == nil {
			_ = os.WriteFile(c.cachePath(), b, 0o644)
		}
	}
	slog.Info("模型元数据已更新", "providers", len(cf.Providers))
}

// build 需要持有锁。
func (c *Client) build(cf cacheFile) {
	c.byKey = map[string]ModelMeta{}
	c.namespaces = map[string]bool{}
	for ns, models := range cf.Providers {
		c.namespaces[ns] = true
		for id, meta := range models {
			key := ns + "/" + strings.ToLower(id)
			if _, exists := c.byKey[key]; !exists {
				c.byKey[key] = meta
			}
			global := "*/" + strings.ToLower(id)
			if _, exists := c.byKey[global]; !exists {
				c.byKey[global] = meta
			}
		}
	}
}
