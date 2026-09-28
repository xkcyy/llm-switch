# LLM Switch 技术设计方案

| 项目 | 内容 |
|---|---|
| 文档版本 | 1.4 |
| 文档状态 | 技术方案草案 |
| 更新日期 | 2026年9月22日 |
| 对应需求 | LLM-Switch-产品需求与功能规划.md（1.6） |
| 目标平台 | Windows 10 / 11（x64） |
| 交付形态 | 单文件 EXE，不超过 50 MB，常驻系统托盘 |
| 推荐技术栈 | Go + 本地 Web UI + 系统托盘（桌面壳可选） |
| 实测基准 | Codex CLI 0.147.0（本机）；Codex 官方配置参考（2026-09）；本机 `~/.codex` 真实文件 |

本版关键更新：

- 按真实 Codex 环境确认：`wire_api` 仅支持 `responses`；`config.toml`、`auth.json`、模型目录（catalog）结构已实测。
- 新增系统托盘与开机启动设计。
- 手动同步与自动同步统一为一套生成与写入逻辑。
- 字段命名对齐 OpenAI / Anthropic / Codex 约定。
- 模型配置字段对齐业界约定：`id` 为上游模型标识、`name` 为显示名，移除 `real_name` 与冗余内部 id。

## 1. 设计目标与约束

### 1.1 设计目标

- 用一个单文件 EXE 提供：供应商/模型管理、本机模型代理、Codex 配置同步、系统托盘常驻。
- 界面复用现有 HTML 原型，后端与界面解耦，浏览器形态和桌面壳形态可切换。
- 模型映射与错误行为完全对齐需求文档 6.5 节，确定性优先。
- Codex 配置读写严格对齐真实 Codex 结构，失败时不破坏现有文件。

### 1.2 硬约束（来自需求与实测）

| 约束 | 说明 |
|---|---|
| 体积 | 单文件 EXE ≤ 50 MB |
| 网络 | 仅监听本机回环地址，不开放局域网 |
| 代理认证 | 本机客户端默认无需 API Key |
| 目标 Agent | 仅 Codex；Claude Code 暂不支持 |
| 常驻形态 | 系统托盘；支持开机启动 |
| 同步逻辑 | 手动与自动共用同一生成与写入逻辑，仅触发方式不同 |
| 不做 | 诊断中心、分层检查、快照与恢复、路由对象 |
| 保留 | 供应商连接测试、模型调用测试 |
| 密钥 | 允许明文存储，界面默认脱敏，不得进入日志 |
| Codex 配置 | config.toml + auth.json + model_catalog_json（真实结构见第 10 节） |
| 协议 | 入口支持 OpenAI Chat、Responses、Anthropic Messages |

### 1.3 设计原则

1. 核心能力与界面解耦：管理能力全部通过本机 HTTP API 暴露，浏览器页面和桌面壳共用同一套 API。
2. 单进程、双服务：一个进程内运行管理面和代理面两个回环 HTTP 服务，另有托盘常驻。
3. 一切配置皆文件：状态保存在用户目录 JSON 文件，原子写入，便于备份和排查。
4. 直通优先、按需转换：入口协议与上游协议一致时按原始字节透传；不一致时仅在已实现矩阵内转换。
5. 写入失败不破坏原文件：所有 Agent 配置写入采用“校验 → 临时文件 → 原子替换”。
6. 非幂等请求不自动重试，避免重复计费和重复副作用。
7. 字段命名对齐业界约定：对外文件完全使用 Codex / OpenAI / Anthropic 字段名；内部配置与 API 使用 snake_case，术语与三家协议保持一致。

## 2. 总体架构

### 2.1 组件图

```text
┌────────────────────────────── llm-switch.exe ──────────────────────────────┐
│                                                                            │
│  系统托盘                          管理面 127.0.0.1:8318                    │
│  ┌──────────────┐                  ┌──────────────────────┐                │
│  │ 打开管理页    │                  │ Web UI（go:embed）    │                │
│  │ 启动/停止代理 │                  │ REST /api/*          │                │
│  │ 配置目录      │                  └──────────┬───────────┘                │
│  │ 开机启动      │                             │                            │
│  │ 退出          │                  ┌──────────▼──────────────────────────┐ │
│  └──────┬───────┘                  │           应用服务层                 │ │
│         │                          │ 供应商 │ 模型 │ 代理控制 │ Codex 同步  │ │
│         │                          │       事件总线                      │ │
│         │                          └──────────┬──────────────────────────┘ │
│         │                                     │                            │
│         │  代理面 127.0.0.1:8317   ┌───────────▼────────────┐               │
│         └─────────────────────────►│ 映射引擎 + 协议适配层  │               │
│                                    │ chat/responses/messages│               │
│                                    └───────────┬────────────┘               │
│                                                │ HTTP                        │
└────────────────────────────────────────────────┼────────────────────────────┘
        ▲                                             ▼
  浏览器（默认形态）                            上游供应商 API
  或桌面壳（可选形态）
```

### 2.2 进程与并发模型

- 单进程、单实例：启动时获取用户目录锁文件（`app.lock`，含 PID），重复启动时打开管理页后退出。
- 托盘消息循环必须运行在操作系统主线程（`runtime.LockOSThread`），其余服务在独立 goroutine 中运行。
- 管理服务、代理服务、Codex 同步器各自独立 goroutine；配置写入用 `sync.RWMutex` 保护。
- 映射索引用 `atomic.Value` 整体替换，代理请求无锁读取。
- 优雅退出：停止接受新请求，等待在途请求（最多 5 秒）后退出并释放锁。

### 2.3 端口与地址

| 服务 | 默认地址 | 说明 |
|---|---|---|
| 代理面 | `http://127.0.0.1:8317` | 供 Codex 访问，免认证；端口可配置 |
| 管理面 | `http://127.0.0.1:8318` | Web UI 与 `/api/*`；Host 校验 + 敏感接口令牌 |

端口被占用时：启动失败并给出明确提示，设置页可修改端口；代理端口变更后自动触发 Codex 配置同步（自动模式）或提示手动保存（手动模式）。

### 2.4 界面形态选项

| 形态 | 实现 | 体积影响 | 依赖 | 结论 |
|---|---|---|---|---|
| A. 本地 Web UI + 系统托盘 | Go 内嵌静态资源，托盘常驻，点击托盘打开浏览器 | 最小（约 15 MB） | 无额外运行时 | **推荐，MVP 默认** |
| B. 桌面壳 Wails（WebView2）+ 托盘 | 同一套页面放进应用窗口，托盘复用同一库 | +2~5 MB | 需要 WebView2（Win10/11 通常自带） | 需要独立窗口时选用 |
| C. 混合 | 默认浏览器 + 可选桌面壳 | 同 B | 同 B | V1 可选 |

托盘与开机启动已纳入 MVP；A 形态即可满足。管理面是纯 HTTP API + 静态资源，形态切换不需要改动后端。

## 3. 技术选型

### 3.1 语言与运行时

- **Go 1.24+**：标准库自带生产级 HTTP 客户端/服务端，适合高并发流式转发；编译产物单文件、体积可控；Windows 系统调用与注册表操作成熟。
- 前端：React 18 + Ant Design 5 + Vite（工程位于 `web/`，构建产物 `web/dist` 由 Go `embed` 内嵌；`prototype/` 保留为交互原型）。
- 托盘：纯 Go 系统托盘库，无额外运行时依赖。

### 3.2 候选栈对比

| 方案 | EXE 体积 | 运行时依赖 | 托盘/开机启动 | 复用 HTML 原型 | 评价 |
|---|---|---|---|---|---|
| Go + 本地 Web UI + 托盘 | 约 15 MB | 无 | 支持 | 直接复用 | 推荐 |
| Go + Wails + 托盘 | 约 18~25 MB | WebView2 | 支持 | 直接复用 | 需要桌面窗口时选用 |
| Rust + Tauri | 约 8~15 MB | WebView2 | 支持 | 直接复用 | 体积更小但开发慢 |
| C# / .NET WPF | 约 60~80 MB | .NET 运行时或自包含 | 支持 | 不适用 | 超出 50 MB 约束 |
| Electron | 150 MB+ | Chromium | 支持 | 直接复用 | 超出约束，不采用 |

### 3.3 依赖库清单（建议）

| 用途 | 库 | 说明 |
|---|---|---|
| HTTP 服务/客户端 | `net/http`、`net/http/httputil` | 代理与 API |
| TOML 读写 | `github.com/pelletier/go-toml/v2` | 解析 + 保留注释（AST 不稳定 API，需封装） |
| JSON | 标准库 `encoding/json` | auth.json、config.json、catalog |
| 系统托盘 | `fyne.io/systray` | Windows 支持良好，纯 Go |
| 注册表与系统调用 | `golang.org/x/sys/windows`、`golang.org/x/sys/windows/registry` | 单实例、开机启动 |
| 日志 | 标准库 `log/slog` + 自研按天滚动 | 控制体积 |
| 静态资源嵌入 | `embed` | 内嵌 Web UI 与托盘图标 |
| 资源与清单 | `go-winres` | 图标、版本号、DPI 感知 |
| 测试 | 标准库 `testing` + `httptest` | 单元与集成测试 |

## 4. 项目结构

```text
cmd/llm-switch/         入口：参数、单实例、托盘、生命周期
internal/
  admin/                管理面 REST API 与前端托管（令牌注入）
  app/                  启动编排、配置变更订阅、Codex 自动同步
  autostart/            开机启动（HKCU Run，--silent）
  buildinfo/            版本与 User-Agent 标识
  codex/                config.toml / auth.json / 模型清单生成与写入、断开接入
  config/               config.json 存储、原子写入、启动自愈
  instance/             单实例互斥体
  logx/                 按天滚动日志
  metadata/             模型元数据在线补全（models.dev + 本地缓存）
  provider/             供应商与模型领域服务（连接测试、模型测试、拉取模型）
  proxy/                代理服务、映射索引
    protocol/           Chat / Responses / Messages 协议转换
  tray/                 系统托盘与图标生成
  winutil/              ShellExecute 封装（无控制台打开浏览器/目录）
web/                    前端工程（Vite + React + antd），构建到 web/dist 供内嵌
prototype/              交互原型（独立运行，模拟数据）
testdata/mock-upstream/ 本地联调模拟上游
docs/                   文档
screenshots/            界面截图
```

## 5. 配置存储设计

### 5.1 位置与文件

```text
%USERPROFILE%\.llm-switch\
├─ config.json        # 设置、供应商、模型（单一配置源）
├─ logs\               # 运行日志与代理日志（按天滚动）
└─ app.lock            # 单实例锁（仅运行期存在）
```

### 5.2 config.json 结构

内部字段统一 snake_case，术语与 OpenAI / Anthropic / Codex 保持一致：

```json
{
  "schema_version": 1,
  "settings": {
    "proxy": { "host": "127.0.0.1", "port": 8317 },
    "admin": { "host": "127.0.0.1", "port": 8318 },
    "log": { "level": "info", "retention_days": 7 },
    "open_browser_on_start": true,
    "auto_start": true,
    "default_model": "DeepSeek/deepseek-chat"
  },
  "providers": [
    {
      "id": "deepseek",
      "name": "DeepSeek",
      "preset": "deepseek",
      "base_url": "https://api.deepseek.com/v1",
      "protocol": "chat",
      "protocols": ["chat"],
      "auth": { "type": "bearer", "api_key": "sk-..." },
      "enabled": true
    }
  ],
  "models": [
    {
      "id": "deepseek-chat",
      "provider_id": "deepseek",
      "name": "DeepSeek Chat",
      "description": "",
      "capabilities": ["tools", "reasoning", "streaming"],
      "context_window": 131072,
      "reasoning": { "default_level": "medium", "levels": ["low", "medium", "high"] },
      "enabled": true
    }
  ]
}
```

字段约束：

- `providers[].id` 是供应商定位项与请求名前缀，在界面上可配置，全局唯一且不允许包含 `/`；`providers[].name` 仅用于展示。
- `protocol` 取值：`chat`、`responses`、`messages`，与 Codex `wire_api` 和 Anthropic Messages 概念对齐。
- `providers[].protocols` 是该上游支持的协议列表（有序、可多选、去重）；`protocol` 为兼容字段，恒等于 `protocols[0]`。代理按「入口命中即直通，否则按序转换」选择协议。
- `auth.type` 取值：`bearer`、`api_key_header`、`custom`。
- `models[].id` 为上游模型标识，在同一供应商内唯一；唯一键为 `(provider_id, id)`，对外请求名为 `供应商ID/id`。
- `models[].name` 为界面显示名，缺省等于 `id`；`capabilities` 使用通用术语（`tools`、`vision`、`reasoning`、`streaming`、`structured_outputs`）。
- `models[].protocol` 可将该模型固定为单一协议（留空表示使用供应商的协议列表；多端点网关的个别模型需要固定时使用）。
- `settings.default_model` 使用 `供应商ID/模型 id` 形式，与请求名保持一致。

### 5.3 写入与并发

- 所有写入走 `config.Store`：加写锁 → 序列化 → 校验 → 写临时文件 → 原子替换（`os.Rename`）。
- 变更后发布事件（`provider.changed`、`model.changed`、`settings.changed`），映射索引与 Codex 同步器订阅。
- `schema_version` 预留迁移入口；未知字段读取后原样保留。

### 5.4 密钥处理

- 明文保存在 `config.json`（需求允许），文件权限默认继承用户目录。
- 列表接口返回脱敏值；完整值仅通过 `GET /api/providers/{id}/secret` 返回。
- 日志、错误信息、Codex 配置文件中一律不得出现供应商密钥；日志输出前统一走脱敏过滤器。

### 5.5 配置自愈与容错

启动时对配置执行一次自愈（`config.Sanitize()`），修复项写入日志并在概览页展示：

| 问题 | 处理 |
|---|---|
| 模型指向不存在的供应商 | 移除该模型 |
| 供应商缺少 ID | 由名称推导（优先英文，全中文时保留中文） |
| 接口协议非法 | 回退为 `chat` |
| 供应商 ID/名称或同供应商内模型 ID 重复 | 保留第一个，忽略后续 |
| 默认模型失效 | 回退到第一个可用模型；没有可用模型则清空 |

配套容错：

- `codex.BuildPlan` 遇到默认模型不在目录中时自动回退并写入 `warnings`，不阻断同步。
- `/api/codex` 对数据问题一律返回 200 + warnings，页面照常展示。
- 前端逐项加载（`Promise.allSettled`）：某一类接口失败只提示该项，其余功能可用。
- 配置为单个 JSON 文件，程序不提供快照/回滚；用户可在改动前自行复制 `config.json` 备份。

## 6. 领域服务设计

### 6.1 供应商

- CRUD、启用/停用、删除时提示关联模型数量。
- 预置类型预填默认协议与默认地址：

| 预置类型 | 默认协议（内部值） | 默认地址 | 说明 |
|---|---|---|---|
| KmAiModelHub | responses | `https://aimodelhub.ai.kmsoft.com.cn/v1` | 企业模型网关，同时支持 Responses 与 Chat |
| OpenCode Go | chat | `https://opencode.ai/zen/go/v1` | 部分模型走 messages / responses，见下方模型级协议 |
| DeepSeek | chat | `https://api.deepseek.com` | OpenAI 兼容，无需 `/v1` |
| OpenAI | responses | `https://api.openai.com/v1` | 可切换 chat |
| Anthropic | messages | `https://api.anthropic.com/v1` | 使用 `x-api-key` 认证 |
| 自定义 | 用户选择 | — | 字段完全可编辑 |

- 连接测试：按协议发最小请求（OpenAI 兼容优先 `GET /models`；Anthropic 发最小 Messages 请求），错误分类：认证失败、网络失败、限流、地址错误、协议错误。

### 6.2 模型

- 手工添加；供应商支持时 `GET /models` 拉取列表。
- 字段：`id`（上游模型标识）、`name`（显示名）、描述、能力标签、上下文窗口（可选）、推理档位（可选）、启用状态；字段直接映射到 Codex catalog 对应项（见 10.4）。
- 模型调用测试：按上游协议发送最小请求（`max_tokens` 限制为 1），返回耗时与错误分类。
- 模型变更（新增/删除/停用、默认模型变更）→ 发布事件 → 触发 Codex 同步。

### 6.3 事件总线

- 进程内同步发布/异步消费（`chan` + 去抖）。
- 事件：`provider.changed`、`model.changed`、`settings.changed`、`proxy.started`、`proxy.stopped`。

## 7. 本地代理设计

### 7.1 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/chat/completions` | OpenAI Chat 入口 |
| POST | `/v1/responses` | OpenAI Responses 入口（Codex 实际使用） |
| POST | `/v1/messages` | Anthropic Messages 入口 |
| GET | `/v1/models` | 当前可用模型清单（id 为 `供应商ID/模型 id`） |
| GET | `/healthz` | 存活检查 |

### 7.2 请求处理管线

1. 校验来源：`RemoteAddr` 必须为回环地址，否则直接拒绝。
2. 读取请求体（上限 32 MB），保留原始字节用于直通。
3. 按路径识别入口协议。
4. 解析并提取 `model`；缺失或为空返回参数错误。
5. 调用映射引擎得到供应商与真实模型；失败按 8.3 返回错误。
6. 校验供应商与模型均启用。
7. 选择上游协议：与入口一致则直通；不一致则查转换矩阵（见 9.2）。
8. 构建上游请求：拼接 `base_url`，注入供应商认证，剥离逐跳头。
9. 发送上游请求：流式响应逐事件回传并 flush；非流式响应缓冲后返回。
10. 记录代理日志（请求模型、命中供应商与真实模型、状态、耗时、失败阶段）。

### 7.3 流式与取消

- 使用 `context` 把客户端断开传播到上游请求，上游连接随之中断。
- SSE 逐事件读取 → 直通或转换 → 立即 `Flush`，保证首字节延迟。
- 流中错误：已经发送 200 时，以协议规定的错误事件结束，不再改状态码。

### 7.4 超时、重试与并发

| 项 | 默认值 | 说明 |
|---|---|---|
| 连接/TLS 超时 | 10 秒 | 可配置 |
| 首字节超时 | 60 秒 | 可配置 |
| 流空闲超时 | 120 秒 | 长时间无事件判定失败 |
| 非流式总超时 | 600 秒 | 可配置 |
| 自动重试 | 关闭 | 非幂等请求不重试 |
| 最大并发 | 32 | 超限返回 503 |

### 7.5 上游请求头约定

认证注入之外，统一补充以下请求头：

- `User-Agent: llm-switch/<版本>`：上游要求客户端自我标识（OpenCode Go 明确要求不得使用通用 HTTP 库名）。
- `x-opencode-session`：会话标识。优先透传客户端的会话头（`x-opencode-session`、`session_id`、`session-id`、`conversation_id`、`thread_id` 等，Codex 使用 `session_id`）；客户端没有时用「模型 + 首条用户消息」生成稳定 ID（`ls-<hash>`），保证同一会话的路由与提示缓存亲和。
- Anthropic 上游自动补齐 `anthropic-version`（可配置）。
- 客户端自带的 `Authorization` 一律忽略并覆盖。

### 7.6 上游认证注入

- `bearer`：写入 `Authorization: Bearer <key>`。
- `api_key_header`：写入自定义头（如 Anthropic 的 `x-api-key`）。
- `custom`：按配置的键值对写入。
- Anthropic 上游自动补齐 `anthropic-version`（可配置）。
- 客户端自带的 `Authorization` 一律忽略并覆盖。

### 7.7 错误模型

统一内部错误（`ProxyError`：阶段、代码、摘要、上游原始信息）→ 按入口协议渲染为对应错误体：

| 场景 | HTTP | OpenAI 风格 code | Anthropic 风格 type |
|---|---|---|---|
| 缺少/非法 model | 400 | `invalid_request_error` | `invalid_request_error` |
| 模型不存在或停用 | 404 | `model_not_found` | `not_found_error` |
| 名称歧义 | 400 | `invalid_request_error`（提示 `供应商/模型`） | `invalid_request_error` |
| 供应商停用 | 400 | `invalid_request_error` | `invalid_request_error` |
| 协议不兼容 | 400 | `invalid_request_error` | `invalid_request_error` |
| 上游认证失败 | 401/403 透传 | 上游错误体 | 上游错误体 |
| 上游限流 | 429 透传 | 上游错误体 | 上游错误体 |
| 上游不可达或 5xx | 502 | `api_error` | `api_error` |
| 上游超时 | 504 | `api_error` | `api_error` |

## 8. 模型映射引擎

### 8.1 索引结构

```go
providerIndex map[string]*Provider          // key: 供应商 ID（名称作为兼容别名）
modelIndex    map[string]map[string]*Model  // 供应商 ID → model.id → model
plainIndex    map[string][]*Model           // model.id → 多个模型（用于无前缀匹配）
```

索引在配置变更后重建，通过 `atomic.Value` 整体替换，读路径无锁。

### 8.2 匹配算法

1. 去除请求 `model` 首尾空白；空值 → 参数错误。
2. 含 `/`：以第一个 `/` 分割；供应商按 ID 匹配（兼容名称别名），不存在/停用 → 供应商不可用；模型不存在/停用 → 模型不可用。
3. 不含 `/`：查 `plainIndex`；0 命中 → 模型不存在；多命中 → 名称歧义；1 命中 → 使用该模型。
4. 不模糊匹配、不纠错、不回退默认供应商。

### 8.3 错误码

| 错误 | 内部代码 | 对外表现 |
|---|---|---|
| 缺少模型名称 | `missing_model` | 400 + 参数错误 |
| 供应商不可用 | `provider_unavailable` | 400 + 明确供应商名称 |
| 模型不可用 | `model_not_found` | 404 + 明确模型名称 |
| 名称歧义 | `model_ambiguous` | 400 + 提示使用 `供应商/模型` |

## 9. 协议适配层

### 9.1 中间表示（IR）

转换路径（入口协议 ≠ 上游协议）经过一份中间表示；同协议直通不经过 IR，原样转发。

- **词汇对齐 Responses**：IR 的条目与流式事件沿用 Responses 的词汇（`message` / `function_call` / `function_call_output` / `custom_tool_call` / `reasoning` 条目，`response.*` 事件），不另立中立术语。
- **形态是 Go 类型**：IR 是内存模型，三种协议的线格式只作为编解码产物；流式是增量事件，字节无法表达。
- **字段集严格等于 Responses 公开字段**：不为上游私有字段开扩展槽。Responses 没有槽位的信息（Chat 的 `reasoning_content` 回传、Anthropic 的 `thinking.signature` 回传）走转换模块内部的侧信道缓存，见 §9.5。
- **每个协议一组适配器**：请求、响应、流三层各一个解码器与一个编码器，成对转换函数不再存在；新增协议只增加一组适配器，不增加方向。
- **迁移状态**：Chat ↔ Responses 已按上述形态落地（`internal/proxy/protocol/codec_chat.go`、`codec_responses.go`、`relay.go`）；Messages 上游暂时仍走成对函数（relay.go 里的 legacy 桥），按 §9.2 的顺序迁移。

直通路径不受 IR 影响：`store`、`include`、`prompt_cache_key`、`text.verbosity`、custom tools、推理条目等仍原样透传。

### 9.2 转换矩阵（MVP）

| 入口 \ 上游 | chat | responses | messages |
|---|---|---|---|
| chat | 直通 | 转换 | 转换 |
| responses | 转换（必需） | 直通 | 转换（必需） |
| messages | 不支持 | 不支持 | 直通 |

- Codex 0.147 起只使用 Responses 入口，因此 **Responses→Chat** 与 **Responses→Messages** 是 MVP 必修路径。
- **协议选择（原生优先）**：供应商声明 `protocols`（有序，可多选），模型可用 `protocol` 固定为单值（多端点网关的个别模型）。解析规则是纯函数、确定性：
  1. 入口协议命中候选列表 → **直通**（不转换，保真度最高：`store`、`include`、`prompt_cache_key`、`text.verbosity`、custom tools、推理条目等全部原样透传）；
  2. 未命中 → 按候选顺序取第一个「转换矩阵里有实现」的协议；
  3. 都不满足 → 400，错误信息列出候选协议。
- 直通/转换与最终协议写入代理记录（`upstream`、`converted`）与日志，便于排查。
- Chat 入口与 Messages 入口保留给其他客户端；Responses↔Messages 反向、Messages→Chat/Responses 未实现，遇到时返回协议不兼容错误。

### 9.3 Codex 侧协议（实测结论）

- 本机 Codex CLI 0.147.0 的 `config.toml` 使用 `wire_api = "responses"`；官方配置参考（2026-09）标注 `chat` 已移除，`responses` 是唯一支持值。
- LLM Switch 生成的 `[model_providers.llm-switch]` 固定写入 `wire_api = "responses"`。
- 因此代理的入口协议实际固定为 Responses，适配重点全部落在上游侧。

### 9.4 字段映射

| 概念 | chat | responses | messages |
|---|---|---|---|
| 系统提示 | `messages[role=system]` | `instructions` | `system` |
| 消息体 | `messages[]` | `input[]` | `messages[]` |
| 工具定义 | `tools[].function` | `tools[]` | `tools[]`（`input_schema`） |
| 工具结果 | `role=tool` | `function_call_output` | `tool_result` |
| 工具选择 | `tool_choice` | `tool_choice` | `tool_choice` |
| 并行工具 | `parallel_tool_calls` | `parallel_tool_calls` | `disable_parallel_tool_use` |
| 最大输出 | `max_tokens` | `max_output_tokens` | `max_tokens` |
| 推理强度 | 上游扩展字段 | `reasoning.effort` | `thinking.budget_tokens`（映射为档位） |
| 推理摘要 | 不适用 | `reasoning.summary` | `thinking` 开关 |
| 输出格式 | `response_format` | `text.format` | 不支持（明确报错或忽略） |
| 存储开关 | 不适用 | `store`（默认 false） | 不适用 |
| 流式事件 | `choices[].delta` | `response.*.delta` | `content_block_delta` |
| 结束原因 | `finish_reason` | `status` / `incomplete_details` | `stop_reason` |
| 用量 | `usage.*_tokens` | `usage.input_tokens/output_tokens` | 同 responses |
| 思考内容 | `reasoning_content` | `reasoning` 条目（summary 承载文本） | `thinking`（透传保留） |
| 自定义工具 | `tools[].type=function`（降级为单 `input` 字符串参数） | `tools[].type=custom` / `custom_tool_call` | 暂未桥接（条目被忽略，可按同样方式降级） |

### 9.5 转换实现约定（Responses → Chat）

Chat Completions 的硬约束是「带 `tool_calls` 的 assistant 消息必须被对应 `tool_call_id` 的 tool 消息立即跟随」，而 Codex 一轮可能产生多个并行工具调用，因此转换层按以下约定实现：

1. **合并工具调用**：连续的 `function_call` / `custom_tool_call` 合并到同一条 assistant 消息的 `tool_calls` 数组，再按调用顺序紧跟 tool 消息；
2. **结果配对**：工具结果按 `call_id` 索引，跨消息也能并回对应的 assistant 消息之后；缺结果补占位、孤立结果丢弃并写入告警（`Record.warnings`）；
3. **思考内容回传（标准路径优先）**：思考型上游（DeepSeek 等）要求下一轮把 `reasoning_content` 原样带回。实测规则：每个带 `tool_calls` 的 assistant 消息都必须带该字段（缺失即 400，覆盖历史里的每一组）；2026-09-23 对 opencode.ai/zen 的对照实验进一步确认——该网关按会话路由到自己的推理状态，命中时忽略客户端回填值，未命中（换会话 ID、重启等）时要求客户端补上该字段，**缺字段必 400，非空占位可通过**。
   - 标准路径：上游的推理文本按 Responses 原生的 `reasoning` 条目下发（文本放 `summary[].text`，与 OpenAI 的 reasoning summary 同形），客户端下一轮原样回传，转换时从回传条目里取文本回填。跨进程有效，不依赖任何代理侧状态。
   - 兜底一（缓存）：按「供应商 + 模型 + 工具调用 ID」缓存 6 小时（单条 64KB、总量 32MB 上限，超限按写入顺序淘汰），覆盖客户端尚未回传的窗口期。流式中断时也落缓存。
   - 兜底二（占位）：两者都未命中时回填**非空占位文本** `(reasoning omitted)`，并写入告警。
   - 兼容 `delta.reasoning`（OpenRouter 风格）别名；
4. **自定义工具桥接**：Codex 桌面版的自定义（自由文本）工具降级为「单个 `input` 字符串」的函数工具传给上游；上游返回同名调用时还原为 `custom_tool_call`（`response.custom_tool_call_input.*` 事件），避免 Codex 无法识别。

### 9.6 已知限制

- 跨协议转换下 `previous_response_id`、`include` 等有状态/原生特性不适用（原生直通时原样透传）。
- 图片等多模态内容仅在直通场景保证；跨协议转换不支持。
- 推理等级无法一一对应时按最接近档位映射，并在日志中记录降级。
- 思考内容缓存是进程内内存态：代理重启后回退为占位文本（请求仍然合法）；占位文本与真实推理不等价，仅供上游校验与上下文连贯。
- 自定义工具的降级桥接要求模型遵守「完整内容放进 `input`」的参数约定；未遵守时按原样透传字符串。

### 9.7 兼容扩展

供应商怪癖（必须回传 `reasoning_content`、会话标识用哪个头名、网关额外要求的请求头）不写在核心路径里，集中为**兼容扩展**（`internal/proxy/protocol/compat/`）。

- **边界**：协议自身固有的行为（Anthropic 的 `anthropic-version`、产品自标识 User-Agent）留在编解码器与调用方；只有供应商特有的怪癖进扩展。
- **机制**（`compat.go` + `registry.go`）：扩展是对象（`Extension` = 名字 + `Match`），时机点由机制定义，干什么由扩展自己决定；机制侧只按接口类型查找，不认识具体扩展。
  - 当前时机点：`BeforeRequest(*Request) error`——上游请求发出前（认证之后）调用，扩展在这里改请求头（`Request.HTTP`）与请求体（`Request.Body`）。`Request` 还提供会话标识、`LookupReasoning`（真实思考内容查询）与告警出口。
  - 新增一个时机点 = 新增一个接口 + 核心侧一个调用点，既有扩展不受影响。
- **实例**（`ext_*.go`，一个文件一条，`init` 自注册）：
  - `reasoning-echo`：在发往 Chat 上游的请求体里，给每条带 `tool_calls` 的 assistant 消息补上非空 `reasoning_content`（缺失即 400）。优先用真实思考内容（代理侧缓存或客户端回传的 `reasoning` 条目），拿不到才回填占位文本并写入告警。自动匹配 preset 为 `deepseek`/`opencode-go`，或 base_url 含 `deepseek`/`opencode.ai`。
  - `opencode-go`：把会话标识写进 `x-opencode-session` 请求头（opencode.ai/zen 缺该头直接判 `MissingSessionID`），也是该供应商后续怪癖的落点。
- **显式挂载**：供应商配置 `extensions: ["reasoning-echo"]`，用于自动匹配判不出来的场景（自建 DeepSeek 兼容网关）。未知名字不生效，但会写进代理记录的告警。
- **新增怪癖**：在内置目录加一条 `Extension` 即可，不改核心路径。

## 10. Codex 配置同步（按实测结构）

### 10.1 文件与托管键

| 文件 | 默认路径 | 托管内容 |
|---|---|---|
| config.toml | `%USERPROFILE%\.codex\config.toml` | `model_provider`、`model`、`model_catalog_json`、`disable_response_storage`、`[model_providers.llm-switch]` |
| auth.json | `%USERPROFILE%\.codex\auth.json` | `OPENAI_API_KEY`（本地代理不校验，写占位值） |
| 模型目录 | `%USERPROFILE%\.codex\llm-switch-models.json` | LLM Switch 当前可用模型（Codex catalog 格式） |

同步只管理上述键；用户已有的其他键（`projects`、`plugins`、`model_reasoning_effort` 等）原样保留。

### 10.2 config.toml 结构与示例

按本机真实结构对齐：

```toml
model_provider = "llm-switch"
model = "DeepSeek/deepseek-chat"
model_catalog_json = 'C:\Users\<用户名>\.codex\llm-switch-models.json'
disable_response_storage = true

[model_providers.llm-switch]
name = "LLM Switch"
base_url = "http://127.0.0.1:8317/v1"
wire_api = "responses"
requires_openai_auth = true
```

要点：

- `wire_api` 固定为 `responses`（Codex 唯一支持值）。
- `requires_openai_auth = true` + auth.json 占位值，避免依赖环境变量。
- `disable_response_storage = true` 与第三方上游兼容（实测配置也使用了该键）。
- `model` 必须与模型目录中的 `slug` 完全一致，否则 Codex 桌面端会把模型标记为“自定义”。
- `model_reasoning_effort`：用户已设置则保留；未设置时按模型目录的 `default_reasoning_level` 写入。

### 10.3 auth.json

```json
{ "OPENAI_API_KEY": "llm-switch-local" }
```

- 文件不存在时创建；已存在且包含用户自己的键值时保留，不覆盖。
- 供应商真实密钥只保存在 LLM Switch 中，不写入 Codex 文件。

### 10.4 模型目录（catalog）

真实结构（依据本机 `km-models.json`，顶层只有 `models` 数组）：

```json
{
  "models": [
    {
      "slug": "DeepSeek/deepseek-chat",
      "display_name": "DeepSeek Chat",
      "description": "DeepSeek 对话模型",
      "default_reasoning_level": "medium",
      "supported_reasoning_levels": [
        { "effort": "low", "description": "轻量推理，优先速度" },
        { "effort": "medium", "description": "均衡速度与推理深度" },
        { "effort": "high", "description": "深度推理，适合复杂任务" }
      ],
      "shell_type": "unified_exec",
      "visibility": "list",
      "supported_in_api": true,
      "priority": 1,
      "availability_nux": null,
      "upgrade": null,
      "model_messages": { "instructions_template": "You are Codex, an AI coding agent." },
      "support_verbosity": false,
      "default_verbosity": null,
      "apply_patch_tool_type": "freeform",
      "truncation_policy": { "mode": "tokens", "limit": 10000 },
      "experimental_supported_tools": []
    }
  ]
}
```

生成默认值：

| 字段 | 取值 |
|---|---|
| `slug` | `供应商ID/模型 id`，必须与 config.toml 的 `model` 一致 |
| `display_name` | 固定等于 `slug`（供应商ID/模型 id），Codex 模型列表与请求都用它 |
| `description` | 模型描述，可为空字符串 |
| `default_reasoning_level` | 模型配置的默认推理档位，默认 `medium` |
| `supported_reasoning_levels` | 模型配置的档位数组，默认 `low/medium/high`，附带中文说明 |
| `shell_type` | 默认 `unified_exec` |
| `visibility` / `supported_in_api` | 固定 `list` / `true` |
| `priority` | 按模型列表顺序递增 |
| `model_messages.instructions_template` | 固定 `You are Codex, an AI coding agent.` |
| 其余字段 | 按上表固定值；模型配置提供 `context_window` 等可选字段时一并写入 |

元数据自动补全：`context_window`、`supported_reasoning_levels`、`default_reasoning_level` 等字段可通过公开数据源自动获取（models.dev / OpenRouter / LiteLLM + Codex 本地缓存），字段映射、匹配优先级、缓存与校验策略见《LLM-Switch-模型元数据来源分析.md》。

注意事项（来自 cc-switch 桌面端问题记录）：

- `slug` 与 `model` 不一致、或 `additional_speed_tiers` 缺失时，Codex 桌面端可能不把模型列入切换菜单。实现时先用真实 Codex 桌面端验证，必要时从 Codex 内置 catalog 模板补齐 `additional_speed_tiers`。

### 10.5 写入流程

1. 读取现有文件内容并解析（TOML/JSON）。
2. 合并托管键，保留未知键与注释（优先 AST 写入；不可行时降级并在界面提示）。
3. 校验生成内容（TOML 可重新解析、JSON 可解析、slug 与 model 一致）。
4. 写临时文件并原子替换；失败时原文件不变，返回错误原因。
5. 回读并比对托管键，必要时用 `codex` 命令做一次冒烟验证。

### 10.6 统一同步逻辑

手动与自动使用同一个生成器与同一条写入路径：

```go
type SyncPlan struct {
    BaseURL      string      // http://127.0.0.1:8317/v1
    DefaultModel string      // 供应商ID/模型 id
    ConfigTOML   string      // 生成的目标内容
    AuthJSON     string      // 生成的目标内容
    Catalog      []CatalogModel
}

func BuildPlan(state *State, overrides *Overrides) (*SyncPlan, error)
func Apply(ctx context.Context, plan *SyncPlan) error // 校验 → 临时文件 → 原子替换 → 回读
```

| 模式 | 触发 | 输入 |
|---|---|---|
| 手动 | 页面点击保存/立即同步 | `BuildPlan(state, 用户编辑的覆盖值)` |
| 自动 | 模型新增/删除/启停、默认模型变更、代理端口变更（去抖 500 ms） | `BuildPlan(state, nil)` |

- 自动模式下页面字段为生成的只读值；手动模式下优先展示实际文件内容，用户修改作为本次覆盖值写入，不改变生成规则。
- 关闭自动同步后不自动写入，也不改动任何 Codex 文件。

### 10.7 页面行为

- 字段：API Key（本地代理不校验，可填占位值）、API 请求地址、默认模型、auth.json 编辑器、config.toml 编辑器。
- 编辑器支持格式化与保存，保存前做语法校验，保存时原子替换。
- cc-switch 的“写入通用配置”选项不纳入 MVP（页面保持最小），配置项预留。

## 11. 管理面 API 与 Web UI

### 11.1 API 一览

| 分组 | 接口 |
|---|---|
| 概览 | `GET /api/overview` |
| 供应商 | `GET/POST /api/providers`，`PUT/DELETE /api/providers/{id}`，`POST /api/providers/{id}/test`，`GET /api/providers/{id}/secret`，`GET /api/providers/presets` |
| 模型 | `GET/POST /api/providers/{provider_id}/models`，`PUT/DELETE /api/providers/{provider_id}/models/{model_id}`，`POST /api/providers/{provider_id}/models/{model_id}/test`，`POST /api/providers/{provider_id}/fetch-models`，`GET /api/models/available` |
| 代理 | `GET /api/proxy/status`，`POST /api/proxy/start`，`POST /api/proxy/stop`，`POST /api/proxy/restart` |
| Codex | `GET /api/codex`（自动开关 + 生成值 + 文件内容），`PUT /api/codex/auto`，`POST /api/codex/sync`（手动触发，可带覆盖值） |
| 设置 | `GET/PUT /api/settings`（含开机启动开关） |
| 系统 | `POST /api/system/open-config-dir`，`POST /api/system/open-log-dir` |

### 11.2 页面与接口

| 页面 | 主要交互 | 主要接口 |
|---|---|---|
| 概览 | 代理状态与启停、模型数量、Codex 自动同步状态、最近错误 | `/api/overview`、`/api/proxy/*` |
| 供应商与模型 | 预置类型、供应商表单、密钥脱敏/查看、连接测试、模型列表、模型调用测试 | `/api/providers/*`（含嵌套模型接口） |
| 模型映射 | 匹配规则说明、可用模型清单；不提供路由创建 | `/api/models/available` |
| 代理 | 启停、端口、监听地址、支持协议、运行状态 | `/api/proxy/*`、`/api/settings` |
| Codex 配置 | 自动同步开关；关闭时手动编辑并保存；开启时只读自动值 | `/api/codex/*` |
| 设置 | 配置目录、日志保留、界面行为、开机启动 | `/api/settings` |

### 11.3 管理面安全加固

- 仅监听回环地址，并校验 `Host` 头只允许 `127.0.0.1[:端口]` 与 `localhost[:端口]`（防 DNS Rebinding）。
- 启动时生成随机令牌（`crypto/rand`，每次运行不同），随管理页 URL 片段传入，页面存入 `sessionStorage`。
- 敏感接口（密钥查看、配置写入、Codex 保存/同步、代理启停、开机启动开关）要求 `X-Admin-Token` 头；只读接口不强制。
- 代理面保持完全免认证。

## 12. 日志设计

- 两个日志流：应用日志（启动、配置变更、同步结果）与代理日志（每次请求一行）。
- 代理日志字段：时间、入口协议、请求模型、命中供应商、命中模型、上游协议、状态码、耗时、失败阶段。
- 按天滚动，保留天数可配置；密钥与认证头统一脱敏；默认不记录请求/响应体，错误时仅记录截断摘要（≤ 1 KB）。
- 设置页提供“打开日志目录”入口，不做诊断中心页面。

## 13. 关键流程

### 13.1 启动、托盘与退出

1. 读取 `config.json`（不存在则生成默认配置）。
2. 获取单实例锁；失败则打开管理页并退出。
3. 启动管理面；按设置启动代理面。
4. 初始化系统托盘（主线程锁定）；按设置打开默认浏览器。
5. 退出：停止代理 → 等待在途请求（≤5 秒）→ 释放锁。

托盘菜单：打开管理页、启动/停止代理、重启代理、打开配置目录、开机启动（勾选）、退出。

### 13.2 开机启动

- 开启：写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，值名 `LLM-Switch`，命令为 `"<EXE 绝对路径>" --silent`（路径带引号）。
- 关闭：删除该值。
- `--silent` 启动不打开浏览器，仅托盘常驻并按设置启动代理。

### 13.3 首次配置

选择预置或自定义供应商 → 填写地址与认证 → 连接测试 → 添加或获取模型 → 模型调用测试 → 启动代理 → 打开 Codex 配置页 → 手动保存或开启自动同步 → Codex 发起真实调用验证。

### 13.4 请求转发

Codex 请求（Responses 入口）→ `model` 提取 → 映射命中 → 上游协议选择（直通或转换）→ 注入认证 → 上游请求 → 流式/非流式回传 → 记录日志。

### 13.5 配置同步（手动与自动同路径）

触发（手动点击保存 / 自动事件去抖）→ `BuildPlan` → 校验 → 原子写入 config.toml、auth.json、catalog → 回读校验 → 失败保留原文件并提示。

## 14. 安全设计

| 项 | 设计 |
|---|---|
| 网络面 | 仅回环监听；不触发 Windows 防火墙入站提示 |
| 代理认证 | 免认证（需求），仅本机可访问 |
| 管理面 | Host 校验 + 随机令牌 + 敏感接口校验 |
| 密钥存储 | 明文（需求允许），界面脱敏，专用接口查看 |
| 日志 | 统一脱敏，不落盘密钥与请求体 |
| 文件写入 | 校验 + 原子替换，失败不破坏原文件；Codex 目录只写托管键 |
| 开机启动 | 仅写 HKCU，不要求管理员权限；路径引号转义 |
| 依赖 | 不引入 WebView2 等外部运行时（形态 A） |

## 15. 构建与发布

- 一键构建：`build.ps1`（前端 `npm run build` → `go test` → 打包 EXE），参数 `-Version`、`-SkipUI`。
- 构建命令：`go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=..."`；`-H=windowsgui` 必须保留，否则双击会出现控制台窗口。
- 打开浏览器/目录使用 `ShellExecute`（`internal/winutil`），不经过 `cmd /c start`，避免 GUI 程序闪黑框。
- 体积预算：当前实测约 9 MB；CI 中设置 45 MB 预警、50 MB 失败的检查。
- 发布物：单个 `LLM-Switch.exe`；无需安装程序；单实例互斥保证不会重复常驻。
- 发布检查清单见《开发与调试指南.md》第 7 节。

## 16. 测试方案

### 16.1 单元测试

- 映射引擎：全部匹配分支与错误码（表驱动，对齐需求 6.5.3）；供应商 ID 定位与名称别名、大小写不敏感。
- 配置存储：原子写入、并发读写、schema 兼容、校验失败不污染内存。
- 配置自愈：坏数据修复（孤儿模型、缺失 ID、非法协议、重复项、默认模型回退）与健康配置不被误改。
- TOML/JSON 合并：托管键覆盖、未知键保留、非法内容拒绝。
- catalog 生成：golden 文件对比真实结构（字段齐全、slug 与 model 一致、display_name 等于 slug、推理档位合法）。

### 16.2 集成测试

- `httptest` 模拟三类上游：正常、401、429、5xx、超时、半途断开。
- SSE 流式：直通与转换两条路径；客户端断开时上游取消。
- 协议转换：golden 文件对比（请求编码、流式事件序列、错误体）。
- 上游请求头：会话头透传（`session_id` → `x-opencode-session`）、缺失时生成稳定兜底 ID、`User-Agent` 为自有标识。
- 模型级协议覆盖：供应商 chat + 模型 responses 时上游路径正确。
- 开机启动：注册表写入/删除、路径含空格、重复开关。

### 16.3 端到端测试

- 本地模拟上游（`testdata/mock-upstream`）：完整链路（新增供应商 → 测试 → 拉取模型 → 导入 → 接入 Codex → 代理转发）。
- 真实 Codex CLI 0.147+：写入配置 → `codex debug models` 解析通过 → 发起对话 → 验证响应。
- Codex 桌面端：验证模型切换菜单可见（catalog 字段满足桌面端要求）。
- 托盘：菜单项行为、开机启动开关、`--silent` 启动、双击无控制台黑框。
- 边界场景：端口冲突、Codex 目录不存在/只读、配置文件损坏、默认模型失效。

### 16.4 验收对应

| 需求验收项 | 验证方式 |
|---|---|
| 1 单 EXE 启动 GUI | 构建产物双击启动 + 浏览器打开管理页 + PE 子系统检查 |
| 2~4 供应商与模型配置、测试 | 集成测试 + 手工验收 |
| 5~9 代理监听、协议、映射、错误、流式 | 代理集成测试 + Codex 真实调用 |
| 10~11 Codex 手动与自动同步 | 端到端测试 + 回读校验 |
| 12 密钥脱敏 | 接口与日志断言 |
| 13 GUI 查看与操作 | 手工验收清单 |
| 14 托盘与开机启动 | 托盘手工测试 + 注册表断言 |
| 15 数据自愈与容错 | 自愈单元测试 + 坏数据启动演练 |

## 17. 风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| Codex 配置或 catalog 格式变化 | 同步失败或模型不显示 | 托管键集中封装；实现前用真实 Codex CLI + 桌面端验证；失败不改原文件 |
| 桌面端 catalog 兼容性（speed tiers 等） | 模型不进切换菜单 | 参照 cc-switch 问题记录补齐字段；保留手工验证步骤 |
| TOML 注释无法保留 | 用户配置格式变化 | 优先 AST 写入；否则提示“注释可能不保留” |
| 协议转换保真度不足 | 调用异常 | 限制在已实现矩阵内；不支持组合明确报错；golden 测试 |
| 端口冲突 | 无法启动 | 明确提示 + 设置页改端口；端口变更触发同步或提示 |
| 托盘库与主线程约束 | 托盘不显示或崩溃 | 托盘运行在主线程，初始化失败时降级为仅浏览器形态 |
| 开机启动被安全软件拦截 | 自启失效 | 设置页显示实际注册表状态，可手动重试 |
| 管理面被本机其他进程访问 | 密钥泄露 | Host 校验 + 随机令牌 + 敏感接口保护 |
| 体积超出 50 MB | 违反约束 | 不引入重依赖；CI 体积检查 |

## 18. 里程碑

| 阶段 | 内容 | 对应需求阶段 |
|---|---|---|
| M1 | 工程骨架、配置存储、托盘与开机启动、管理 API 与 Web UI 框架、供应商/模型管理与测试 | MVP-1 |
| M2 | 代理服务、映射引擎、Responses 入口、直通、流式与取消、错误模型 | MVP-2 |
| M3 | 关键转换（Responses→Chat、Responses→Messages）、工具调用与用量映射 | MVP-2 |
| M4 | Codex 配置生成（config.toml、auth.json、catalog）、手动/自动统一同步、真实 Codex 验证 | MVP-3 |
| M5 | 打包与体积校验、端到端测试、验收清单执行 | MVP-4 |

## 19. 待确认问题

已确认：`wire_api = "responses"`（唯一支持值）；catalog 真实结构（含 `display_name` = slug）；默认端口 8317/8318；手动与自动共用同一逻辑；托盘与开机启动纳入 MVP；预置地址与 KmAiModelHub 接入；上游请求头（UA + 会话标识）方案。

剩余待验证：

1. catalog 中 `shell_type`、`truncation_policy`、`additional_speed_tiers` 等默认值的最佳来源（内置模板 vs 固定值），以 Codex 桌面端实际表现为准；推理档位枚举的归一化规则同样需要桌面端验证（见《LLM-Switch-模型元数据来源分析.md》）。
2. 是否统一管理 `model_reasoning_effort`（当前策略：用户已设置则保留）。
3. `requires_openai_auth = true` 在 Codex 新版本中的行为是否需要调整为 `env_key` 方案。
4. 真实供应商账号下的端到端调用（各供应商协议差异与限流表现）。

## 20. 实现状态

| 模块 | 状态 | 说明 |
|---|---|---|
| 配置存储与自愈 | 已完成 | 原子写入、失败不污染内存、启动自愈与修复提示 |
| 供应商与模型管理 | 已完成 | 预置类型（KmAiModelHub / OpenCode Go / DeepSeek / OpenAI / Anthropic / 自定义）、连接与调用测试、拉取模型、协议建议 |
| 密钥处理 | 已完成 | 列表脱敏、专用接口按需返回完整值、日志过滤 |
| 本机代理 | 已完成 | 三类入口、映射、认证注入、请求头约定、流式转发、错误映射 |
| 协议转换 | 已完成 | 直通 + Responses↔Chat + Chat→Messages + Responses→Messages（含 SSE）；工具调用合并/结果配对/思考内容回填/自定义工具桥接 |
| 供应商多协议（原生优先） | 已完成 | `providers[].protocols`（有序多选）+ 原生直通优先 + 记录 `converted`/`warnings`；`models[].protocol` 固定单协议 |
| Codex 配置同步 | 已完成 | 一键接入、自动同步、断开接入、回读校验；slug 与 display_name 均为 `供应商ID/模型ID` |
| 模型元数据补全 | 已完成 | models.dev + 本地缓存，命名空间优先、失败降级 |
| 系统托盘与开机启动 | 已完成 | 托盘菜单、HKCU Run、`--silent` |
| 管理界面 | 已完成 | Ant Design 六页改四页；令牌与 Host 校验 |
| 无控制台黑框 | 已完成 | ShellExecute 打开浏览器/目录 |
| 桌面壳形态（Wails） | 未实现 | 预留，后端无需改动 |
