# LLM Switch 模型元数据来源分析

**主题：context_window 与 model_reasoning_effort 的互联网自动获取**

| 项目 | 内容 |
|---|---|
| 文档版本 | 1.1 |
| 更新日期 | 2026年9月22日 |
| 关联文档 | LLM-Switch-技术设计方案.md（1.3） |
| 实现状态 | 已实现（`internal/metadata`，models.dev + 本地缓存） |
| 实测环境 | Windows 本机；Codex CLI 0.147.0；数据源实测于 2026-09-21 |

## 1. 结论摘要

- 上下文窗口与推理档位都可以通过公开免费数据源自动获取，无需手工维护。
- 推荐三源组合：**models.dev**（主源，含推理档位枚举）、**OpenRouter**（辅源，含默认档位）、**LiteLLM**（兜底，覆盖最广）。
- OpenAI 官方模型直接读取 **Codex 自己的模型缓存 / 内置目录**，它与 Codex 行为完全一致，同时提供目录字段模板。
- 生成模型目录后必须用 `codex debug models` 校验；实测该命令能报出缺失的必填字段（例如 `supports_parallel_tool_calls`）。

## 2. 数据源实测结果

| 来源 | 接口 | 上下文窗口字段 | 推理档位字段 | 覆盖规模 | 认证 |
|---|---|---|---|---|---|
| models.dev | `https://models.dev/api.json`（约 4.7 MB） | `limit.context`、`limit.output` | `reasoning`、`reasoning_options[]`（`toggle` / `effort` 枚举 / `budget_tokens`） | 222 个供应商 | 无 |
| OpenRouter | `https://openrouter.ai/api/v1/models` | `context_length`、`top_provider.max_completion_tokens` | `reasoning.supported_efforts`、`reasoning.default_effort`、`supported_parameters`（含 `reasoning_effort`） | 446 个模型 | 无 |
| LiteLLM | `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`（约 2.8 MB；CDN 镜像 `cdn.jsdelivr.net/gh/BerriAI/litellm@main/...`） | `max_input_tokens`、`max_output_tokens` | `supports_reasoning`、`reasoning_effort_levels`、`default_reasoning_effort`、按档位的能力开关（`supports_xhigh_reasoning_effort` 等） | 4325 条 | 无 |
| Codex 模型缓存 | `%USERPROFILE%\.codex\models_cache.json`（由 Codex 联网拉取后缓存） | `context_window`、`max_context_window`、`effective_context_window_percent` | `default_reasoning_level`、`supported_reasoning_levels[]` | 本机 11 个官方模型 | 无（本地文件） |
| Codex 内置目录 | `codex debug models` | 同上，字段更全 | 同上 | 官方模型 + 当前自定义目录 | 无（本地命令） |

### 2.1 models.dev 实测样例

```json
{
  "claude-sonnet-4-6": {
    "limit": { "context": 1000000, "output": 128000 },
    "reasoning": true,
    "reasoning_options": [
      { "type": "effort", "values": ["low", "medium", "high", "max"] },
      { "type": "budget_tokens", "min": 1024 }
    ],
    "tool_call": true,
    "modalities": { "input": ["text", "image", "pdf"], "output": ["text"] },
    "interleaved": { "field": "reasoning_content" }
  }
}
```

同一来源中 `deepseek-v4-flash-vision-exp` 的档位为 `["low","high","max"]`，`gpt-5.6-sol` 为 `["none","low","medium","high","xhigh","max"]`，上下文 1,050,000。

### 2.2 OpenRouter 实测样例

```json
{
  "id": "anthropic/claude-sonnet-4.6",
  "context_length": 1000000,
  "top_provider": { "context_length": 1000000, "max_completion_tokens": 128000 },
  "supported_parameters": ["reasoning", "reasoning_effort", "verbosity", "tools", "..."],
  "reasoning": { "mandatory": false, "supported_efforts": ["max", "high", "medium", "low"], "default_effort": "medium" }
}
```

OpenRouter 是三个来源中唯一直接给出 **默认档位**（`default_effort`）的接口。

### 2.3 LiteLLM 实测样例

`model_prices_and_context_window.json` 共 4325 条，`deepseek/deepseek-chat` 条目包含：

```json
{
  "max_input_tokens": 131072,
  "max_output_tokens": 8192,
  "supports_reasoning": true,
  "reasoning_effort_levels": ["..."],
  "default_reasoning_effort": "...",
  "supports_parallel_function_calling": true,
  "supports_vision": false
}
```

### 2.4 Codex 本地目录实测

| 模型 | context_window | effective % | 推理档位 | 并行工具 |
|---|---|---|---|---|
| gpt-5.2-codex | 272,000 | 95 | low/medium/high/xhigh | true |
| gpt-5.1-codex-max | 272,000 | 95 | low/medium/high/xhigh | false |
| gpt-oss-120b | 128,000 | 95 | low/medium/high | false |

注意：Codex 的 `context_window` 是**有效工作窗口**（272k），不是模型原始上限（`max_context_window` 为 1M）。官方模型应沿用 Codex 缓存值，不要直接使用数据源的原始上限。

## 3. 字段映射到 Codex 模型目录

| Codex catalog 字段 | 首选来源 | 备选 | 默认值 |
|---|---|---|---|
| `context_window` | Codex 缓存（官方模型） | models.dev `limit.context` → OpenRouter `context_length` → LiteLLM `max_input_tokens` | 128000 |
| `default_reasoning_level` | OpenRouter `reasoning.default_effort` | LiteLLM `default_reasoning_effort` | `medium`（不在档位列表时取列表中间值） |
| `supported_reasoning_levels` | models.dev `reasoning_options[type=effort].values` | OpenRouter `supported_efforts` / LiteLLM `reasoning_effort_levels` | `low/medium/high` |
| `description` | models.dev `description` | OpenRouter `description` | 空 |
| `input_modalities` | models.dev `modalities.input` | OpenRouter `architecture.input_modalities` | `["text"]` |
| `supports_parallel_tool_calls` | LiteLLM `supports_parallel_function_calling` | — | `true`（**必填**） |
| `max_output_tokens`（模型配置） | models.dev `limit.output` | LiteLLM `max_output_tokens` | 不写入 Codex 目录，仅用于界面与后续限流 |

> 实现说明（0.4.x）：当前已落地的自动补全字段为 `context_window`（`limit.context`）与 `max_output_tokens`（`limit.output`）、推理档位（`reasoning_options[type=effort].values`）与描述；`default_reasoning_level` 在导入时按档位列表取中间值，其余字段使用固定默认值。

推理机制（`reasoning_options` 的 `toggle` / `budget_tokens` / `interleaved`）建议保留在 LLM Switch 内部，用于代理运行时把 `reasoning.effort` 映射为上游参数（DeepSeek 的 `reasoning_effort`、Anthropic 的 `thinking` 预算等）。

## 4. 匹配与合并策略

已实现（0.4.x，仅 models.dev 一个来源）：

1. 命名空间映射：预置类型直接对应 models.dev provider id（`deepseek`、`anthropic`、`openai`、`opencode-go`）；聚合类网关（如 KmAiModelHub）视为未知命名空间。
2. 匹配顺序：命名空间内精确匹配 → 命名空间内归一化匹配（小写、去日期后缀）；未知命名空间时退化为全局精确匹配 → 全局归一化匹配。
3. **不猜测**：命名空间存在但模型未收录时直接返回未命中，避免用其他供应商的同名模型填错默认值。
4. 手动值永远优先于自动值；未命中时保留手动填写或使用默认值。

后续可选增强：接入 OpenRouter（提供 `default_effort`）与 LiteLLM（覆盖最广）作为备选来源，按 models.dev → OpenRouter → LiteLLM 取第一个命中；官方模型优先使用 Codex 本地缓存值。

## 5. 落地设计

已实现：

- 缓存文件：`%USERPROFILE%\.llm-switch\metadata\modelsdev.json`，含 `fetched_at`。
- 刷新策略：首次查询时加载缓存；缓存超过 7 天或不存在时联网刷新；失败时静默沿用缓存/默认值。
- 补全时机：从上游拉取模型列表时逐条查询，导入时写入模型配置。
- 不阻塞：元数据拉取失败不影响代理与 Codex 同步，缺失字段留空（界面显示 `—`），可手动补充。
- 校验闭环：生成的模型目录可用 `codex debug models` 校验（实测能发现缺失必填字段）。
- 礼貌请求：每次刷新只请求一次全量数据（约 4.7 MB）。

## 6. 注意事项

- 档位枚举需要归一化：数据源使用 `none/low/medium/high/xhigh/max` 等值，Codex 内部枚举与其存在差异（桌面端为 6 档：极低/低/中/高/超高/最高）。归一化规则需用 `codex debug models` 和桌面端实际表现验证。
- 自动值不能替代校验：`supports_parallel_tool_calls` 实测为必填，缺失会导致整个目录解析失败（本机 km 清单即存在该问题）。
- 第三方数据对新模型存在滞后，未命中时留空并提示用户手动补充（不猜测、不套用其他供应商的值）。
- 数据均为公开来源（LiteLLM 仓库 MIT、models.dev 为开源项目数据、OpenRouter 公开接口）；商用前复核许可与使用条款。
- 不建议抓取厂商文档页面作为主方案（结构易变），可作为供应商专属目录模板的补充。
