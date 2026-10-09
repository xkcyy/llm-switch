# WorkBuddy 接入分析与方案

| 项目 | 内容 |
|---|---|
| 结论 | **可行，走配置文件接入**。WorkBuddy 的自定义模型落在 `~/.workbuddy/models.json`，只走 OpenAI 兼容的 Chat Completions；改文件后 WorkBuddy 会监听到变化，下一次请求即生效，无需重启 |
| 依据 | 本机 WorkBuddy 客户端 `app.asar`（模型设置面板、provider 目录、`isValidLocalCustomModel`、`getUserConfigPath`、`watchFile`）与真实 `~/.workbuddy/models.json`；旁证：开源项目 WorkDaddy 直接读写同一文件 |
| 交付物 | `internal/workbuddy`、页面「接入 WorkBuddy」（`web/src/pages/WorkBuddy.jsx`）、`GET/PUT/POST /api/workbuddy*` |
| 未验证 | 未用真实账号跑一次端到端模型调用（见「待验证」） |

## 1. 结论

WorkBuddy（腾讯 CodeBuddy 系的桌面端 Agent）在「设置 → 模型 → 添加模型 → 自定义 API」里支持接入外部模型，只认 OpenAI 兼容的 Chat Completions（界面提示 `OpenAI-compatible API required`）。每条自定义模型持久化到用户目录的 `models.json`：

- Windows / macOS / Linux 统一为 `~/.workbuddy/models.json`；WorkBuddy AI（国际版）同目录，CodeBuddy 才是 `~/.codebuddy/models.json`；
- 可用环境变量 `WORKBUDDY_CONFIG_DIR` / `CODEBUDDY_CONFIG_DIR` 覆盖目录（客户端自身支持的约定）；
- 文件格式是 JSON 数组，或 `{ "models": [...] }`；两种都被客户端接受；
- 客户端**监听该文件**（`models.json` 的文件监听 + 项目级 `.workbuddy/models.json`），改动后重读，不需要重启。

因此接入方式是**文件写入**：与本机代理的 `/v1/chat/completions` 对接，模型名直接用 `供应商ID/模型ID`，真实密钥仍留在 LLM Switch，配置里只放占位值。

## 2. WorkBuddy 关键事实

| 项 | 值 | 来源 |
|---|---|---|
| 配置文件 | `~/.workbuddy/models.json` | `getUserConfigPath`（产品名含 workbuddy 时用 `.workbuddy`） |
| 覆盖变量 | `WORKBUDDY_CONFIG_DIR` / `CODEBUDDY_CONFIG_DIR` | 同上 |
| 文件格式 | 数组，或含 `models` 数组的对象 | `getLocalCustomModels`、`readModelsFile` |
| 生效方式 | 监听文件变化，下一次请求重读 | 模型配置 watcher（`setupFileWatchers` / `watchFile`） |
| 协议 | 仅 OpenAI 兼容 Chat Completions | 添加模型弹窗 `settings.models.modal.protocolNotice` |
| 条目地址字段 | `url`，默认自动补 `/chat/completions` | `chatEndpoint` 语义 + `useCustomProtocol` 提示 |
| 条目标记 | 额外字段会被保留（校验只按已知字段类型判定） | `isValidLocalCustomModel` |

单条自定义模型的字段（`LocalCustomModelConfig`）：

```json
{
  "id": "DeepSeek/deepseek-chat",       // 请求时发送的 model 名
  "name": "DeepSeek/deepseek-chat",     // 选择器显示名
  "vendor": "LLM Switch",
  "url": "http://127.0.0.1:8317/v1/chat/completions",
  "apiKey": "llm-switch-local",
  "supportsToolCall": true,
  "supportsImages": false,
  "supportsReasoning": true,
  "useCustomProtocol": true,
  "maxInputTokens": 128000,
  "maxOutputTokens": 16384,
  "reasoning": { "defaultEffort": "high", "supportedEfforts": ["low", "high", "max"] }
}
```

要点：

- `useCustomProtocol: true` 的语义是「地址原样使用、不再自动拼接 `/chat/completions`」；本工具写入完整地址并开启它，行为确定。
- `reasoning` 的原生档位集合为 `minimal` / `low` / `medium` / `high` / `xhigh` / `max`（`REASONING_EFFORTS`），与本机代理的档位词一一对应（`off` / `none` 不进入集合）。
- 模型的 `id` 就是请求里发送的 `model` 名；`custom-local:` 前缀是客户端的可选内部特性（发送前会剥离），本工具不使用，直接用 `供应商ID/模型ID`。

## 3. 实现要点

- **节点级合并**：解析为「顶层数组」或「含 `models` 的对象」，逐条保留用户已有条目（原始字节、字段顺序、API Key 都不动），只增删本工具写入的条目。
- **所有权判定**：条目带 `llmSwitchManaged` 标记，或 `url` 指向回环地址且端口等于本机代理端口时，视为本工具写入；其余自定义模型（例如用户自建的 Ollama）一律不动。断开接入只删自己的条目。
- **写入内容**：模型名 `供应商ID/模型ID`；`url` 指向 `http://<代理 Host>:<Port>/v1/chat/completions`；`apiKey` 放占位值；工具调用、图片输入、推理档位、上下文窗口、最大输出按模型元数据写入。
- **原子写入**：写入前对比字节，内容一致不落盘；先写临时文件再 `rename`，避免半截文件。
- **失败隔离**：现有文件不是合法 JSON、或 `models` 不是数组时只报错，不覆盖原文件。
- **自动同步**：与 Codex / OpenCode / dsh 相同的去抖机制，仅在已接入时维护，不主动创建 `models.json`。

## 4. 接口与界面

| 接口 | 说明 |
|---|---|
| `GET /api/workbuddy` | 状态：是否接入、模型、其它自定义模型、路径、文件格式、生成内容、告警 |
| `PUT /api/workbuddy/auto` | 自动同步开关 |
| `POST /api/workbuddy/sync` | 一键接入 / 重新同步 |
| `POST /api/workbuddy/disconnect` | 断开，只移除本工具写入的自定义模型 |

页面「接入 WorkBuddy」：未接入时是引导页（检查供应商 / 模型 / 代理 / 配置目录，预览将写入的 JSON）；接入后展示模型数、请求地址、文件格式、配置文件路径、自动同步开关，并提供重新同步、测试一次调用、断开。

## 5. 待验证

1. 真实 WorkBuddy 里「设置 → 模型」出现「自定义模型」分组与全部 `供应商ID/模型ID`；
2. 新会话发一条，代理日志出现 `entry=chat model=<供应商ID/模型ID> status=200`；
3. 图片输入模型附加图片能通过；
4. 本工具增删模型后，WorkBuddy 选择器不重启即更新；
5. 连接后确认文件里其它自定义模型与 API Key 未被破坏。

## 6. 风险

| 风险 | 影响 | 应对 |
|---|---|---|
| 客户端改版字段或路径 | 写入失效 | 适配器隔离在 `internal/workbuddy`；解析失败显式报错，不静默重写 |
| 与模型设置页并发写 | 互相覆盖 | 写前重读、原子替换；改动后由客户端自行重读 |
| 客户端把未知字段丢弃 | 所有权标记丢失 | 兜底用「回环地址 + 代理端口」判定所有权 |
| 用户自建的本机模型同在回环地址 | 误判 / 误删 | 端口必须等于本机代理端口；非同端口一律不动 |
