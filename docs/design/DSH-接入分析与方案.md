# DeepSeek Harness（dsh）接入分析与方案

| 项目 | 内容 |
|---|---|
| 结论 | **可行，走配置文件接入**。dsh 有原生「自定义模型 API」provider 机制，三种协议与本机代理一一对应；改 profile patch 后下一次请求即生效，无需重启 |
| 依据 | dsh 官方文档 `docs/user/guide/providers.zh.md`、`packages/llm/llm-pi-ai`、`packages/boot/app-boot`、`apps/desktop/README.zh.md`（仓库 master，2026-09-28） |
| 交付物 | `internal/dsh`、页面「接入 DeepSeek Harness」（`web/src/pages/Dsh.jsx`）、`GET/PUT/POST /api/dsh*` |
| 未验证 | 真实桌面端的端到端调用与图片输入（本机未安装 dsh，见「待验证」） |

## 1. 结论

dsh 是 Cordis 插件架构的 agent harness，模型提供方由 `llm-pi-ai` 适配器承载。它的「设置 → 模型 → 自定义模型 API」正是为「中转站、公司网关、自建服务器」设计的，字段与本机代理高度吻合：

- **协议**：表单提供 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 三种，在 patch 里分别存为 `openai-completions`、`openai-responses`、`anthropic-messages`——与 LLM Switch 的 `chat` / `responses` / `messages` 三个入口一一对应；
- **地址**：`baseURL` 填本机代理的 `/v1`，模型请求全部经本机转发；
- **模型**：provider 的 `models` 列表就是模型选择器里可选的模型，模型 `id` 是请求时发送的模型名，支持包含 `/`（provider 与 model 分开传递，不做字符串拼接）；
- **生效**：适配器在下一次请求时重读配置，**不需要重启**；
- **落盘**：模型页的写入通过 settings 服务持久化到当前 profile 的 Cordis patch，即 `$DSH_HOME/profiles/<profile>/cordis.patch.yml`。

因此接入方式是**文件写入**，不是 Trae 那种 CDP 界面驱动：patch 是官方事实源，dsh 自身也支持「打开配置文件」直接编辑。

## 2. dsh 关键事实

| 项 | 值 |
|---|---|
| 主目录 | `DSH_HOME`，默认 `~/.dsh`（Windows 同） |
| profile 目录 | `$DSH_HOME/profiles/<profile>` |
| 用户 patch | `$DSH_HOME/profiles/<profile>/cordis.patch.yml` |
| 桌面端 profile | `desktop`（Electron 壳独占） |
| Web 端 profile | `web`（`dsh web` 默认） |
| 桌面端端口 | 19387（Web 为 3080） |
| 内置适配器条目 | `- id: llm-pi-ai` / `name: '@deepseek-ai/dsh-llm-pi-ai'` |
| provider 路由键 | `config.providers.<route>`，如 `llm-switch` |
| 凭据 | `$DSH_HOME/.credentials.yaml`，或 provider 的 `headers` 静态头 |
| patch 结构 | 顶层 YAML 列表，条目支持 `id` 定向覆盖与 `insert` 插入 |
| patch 保真 | dsh 自身编辑器保留注释与 `!!js` 表达式；`go yaml.v3` 节点级编辑同样保留 |

patch 应用顺序（低到高）：bundle 层 → profile 自身 `cordis.patch.yml` → home patch `$DSH_HOME/cordis.patch.yml` → 命令行 overlay。本接入写 profile patch，与 dsh 模型页同一份文档。

## 3. 写入内容

只维护 `llm-pi-ai` 条目下 `providers["llm-switch"]` 一段：

```yaml
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      llm-switch:
        displayName: LLM Switch
        api: openai-completions
        baseURL: http://127.0.0.1:8317/v1
        headers:
          Authorization: Bearer llm-switch-local
        models:
          - id: DeepSeek/deepseek-chat
            name: DeepSeek/deepseek-chat
            contextWindow: 128000
            maxTokens: 8192
            input: [text, image]
            reasoningEfforts:
              off:
              high: high
```

- **模型名**：直接用 `供应商ID/模型ID`（`config.ModelSlug`），与本机代理的映射契约一致，同名模型不歧义。
- **协议**：固定 `openai-completions`；模型级 `protocol`（如固定 responses 的模型）由本机代理转换，dsh 侧无需感知。
- **凭据**：本机代理不校验密钥，但 pi-ai 的 OpenAI 兼容实现要求密钥或 `Authorization` 头；用 `headers.Authorization` 放占位值，**不写 `.credentials.yaml`**，避免碰 dsh 的凭据存储。
- **模型元数据**：上下文窗口写 `contextWindow`、最大输出写 `maxTokens`、视觉能力写 `input: [text, image]`、推理档位写 `reasoningEfforts`（`none`/`off` 映射为空的 `off`，其余按 `minimal`…`max` 原样）。

## 4. 实现要点

- **YAML 节点级合并**：用 `gopkg.in/yaml.v3` 的 `yaml.Node` 读取—修改—编码。只增删 `llm-switch` 节点，其它条目、provider、注释与 `!!js` 表达式原样保留；写入前对比字节，内容一致则不落盘，避免自动同步空转。
- **条目合并**：按 `id: llm-pi-ai` 定位现有条目并合并 provider；不存在时追加带 `name` 的条目。断开时只删 `llm-switch`，providers 为空再删 `providers`/`config`；若条目只剩 `id`/`name`（说明是本次接入新建的），整条移除。
- **profile 选择**：设置里选 `desktop`（默认）或 `web`，并列出磁盘上已存在的 profile 供选择。profile 目录不存在时给出「请先启动一次桌面端」的提示，不强行创建。
- **自动同步**：与 Codex / OpenCode 相同的去抖机制，仅在已接入时维护（`Connected(profile)` 为真才写），不主动创建 profile。
- **失败隔离**：现有 patch 不是 YAML 列表、解析失败或写入失败时只报错，不破坏原文件。

## 5. 接口与界面

| 接口 | 说明 |
|---|---|
| `GET /api/dsh` | 状态：是否接入、模型、profile、路径、生成内容、告警 |
| `PUT /api/dsh/auto` | 自动同步开关 |
| `PUT /api/dsh/profile` | 目标 profile |
| `POST /api/dsh/sync` | 一键接入 / 重新同步 |
| `POST /api/dsh/disconnect` | 断开，只移除本工具写入的 provider |

页面「接入 DeepSeek Harness」：未接入时是引导页（检查供应商/模型/代理/profile，预览将写入的 YAML）；接入后展示模型数、地址、配置文件路径、profile 选择、自动同步开关，并提供重新同步、测试一次调用、断开。

## 6. 待验证

本机未安装 dsh，以下需在真实桌面端复核：

1. 真实桌面端接入后，「设置 → 模型」出现 `LLM Switch` 与全部模型；
2. 新会话发一条，代理日志出现 `entry=chat model=<供应商ID/模型ID> status=200`；
3. 图片输入模型附加图片能通过；
4. 本工具增删模型后，dsh 选择器不重启即更新；
5. 连接后确认 patch 中其它条目的注释与 `!!js` 表达式未被破坏；
6. dsh 运行中外部改 patch 是否被 HMR 接受、与模型页写入是否互相覆盖。

## 7. 风险

| 风险 | 影响 | 应对 |
|---|---|---|
| developer preview 破坏性变更 | patch 结构或字段改名 | 适配器隔离在 `internal/dsh`；解析失败显式报错，不静默重写；按真实版本回归 |
| YAML 往返格式变化 | 用户 patch 被重排 | 节点级编辑保留注释与表达式；内容一致不落盘；建议接入前备份 |
| 与模型页并发写 | 互相覆盖 | 写前重读、原子替换；必要时提示先关闭 dsh |
| 桌面端 profile 锁 | 写入被 dsh 覆盖 | dsh 会重读 patch；以 HMR 结果为准，冲突时以 dsh 模型页为准 |
| 占位凭据被网关拒绝 | 请求 401 | 本机代理不校验；若将来加鉴权，改为写 `.credentials.yaml` 或真实 `apiKeyEnv` |