# LLM Switch

Windows 本地模型代理：统一管理多个模型供应商，通过一个仅监听本机的代理向 Codex、OpenCode 提供模型服务，并自动维护它们的配置（Codex：config.toml / auth.json / 模型清单；OpenCode：opencode.json）。

界面按"默认即正确、用户零思考"设计：选预设、粘贴 API Key，其余全部自动完成。

- 文档导航：[docs/](docs/README.md)
- 功能说明（这个工具能做什么）：[docs/功能说明.md](docs/功能说明.md)
- 使用指南（怎么用）：[docs/使用指南.md](docs/使用指南.md)
- 设计文档：[docs/design/](docs/design/)
  - 产品需求：[docs/design/LLM-Switch-产品需求与功能规划.md](docs/design/LLM-Switch-产品需求与功能规划.md)
  - 技术方案：[docs/design/LLM-Switch-技术设计方案.md](docs/design/LLM-Switch-技术设计方案.md)
  - 模型元数据来源分析：[docs/design/LLM-Switch-模型元数据来源分析.md](docs/design/LLM-Switch-模型元数据来源分析.md)
  - 开发与调试指南：[docs/design/开发与调试指南.md](docs/design/开发与调试指南.md)
  - 变更记录：[docs/design/变更记录.md](docs/design/变更记录.md)
- 交互原型（Ant Design，可独立运行）：[prototype/](prototype/README.md)

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.24+（实测 1.26）：代理、管理 API、Codex 配置读写、模型元数据补全 |
| 前端 | Vite 5 + React 18 + Ant Design 5（`web/`），构建产物由 Go `embed` 内嵌 |
| 运行 | 单文件 EXE，系统托盘常驻，支持开机启动；无 WebView2 等外部依赖 |

## 构建与运行

```powershell
# 一键构建（前端 + 测试 + EXE），产物 dist/LLM-Switch.exe
# 发行版使用 -H=windowsgui（Windows GUI 子系统），双击不会出现控制台黑框
powershell -ExecutionPolicy Bypass -File build.ps1 -Version 0.4.2

# 只构建后端（前端已构建过）
powershell -ExecutionPolicy Bypass -File build.ps1 -SkipUI

# 开发模式
cd web; npm run dev            # 前端开发服务器（需手动指向 8318，或用生产联调）
go run ./cmd/llm-switch --no-tray

# 运行
.\dist\LLM-Switch.exe            # 托盘常驻并打开管理页
.\dist\LLM-Switch.exe --silent   # 静默启动（开机启动使用）
.\dist\LLM-Switch.exe --no-tray  # 调试：不启动托盘
.\dist\LLM-Switch.exe --version
```

启动后：管理界面 `http://127.0.0.1:8318`（自动打开），模型代理 `http://127.0.0.1:8317`，配置目录 `%USERPROFILE%\.llm-switch\`。

本地联调可用模拟上游（OpenAI 兼容）：`go run ./testdata/mock-upstream -addr 127.0.0.1:9911`。

## 目录结构

```text
cmd/llm-switch/       程序入口（单实例、托盘、生命周期）
internal/
  admin/              管理面 REST API（/api/*）与前端托管
  app/                启动编排、配置变更订阅、Codex / OpenCode 自动同步
  autostart/          开机启动（HKCU Run）
  buildinfo/          版本与 User-Agent 标识
  cdp/                极简 CDP 客户端（本机 Electron 调试端口：执行 JS、真实鼠标/按键）
  codex/              config.toml / auth.json / 模型清单生成与写入、断开接入
  opencode/           opencode.json 的 provider.llm-switch 段生成与写入、断开接入
  trae/               Trae SOLO 接入：路径探测、argv.json 调试端口、界面驱动、任务进度
  config/             config.json 存储（原子写入、启动自愈）
  instance/           单实例互斥体
  logx/               按天滚动日志
  metadata/           模型元数据在线补全（models.dev，本地缓存、失败降级）
  provider/           供应商与模型领域服务（连接测试、模型测试、拉取模型）
  proxy/              代理服务、映射索引
    protocol/         Chat / Responses / Messages 协议转换（请求、响应、流式）
  tray/               系统托盘与图标生成
  winutil/            ShellExecute 封装（无控制台打开浏览器/目录）
web/                  前端工程（Vite + React + antd），构建到 web/dist 并内嵌
prototype/            交互原型（独立运行，模拟数据）
testdata/mock-upstream/ 本地联调用的模拟上游
tools/trae-solo/      Trae 模型同步脚本（排错参照，产品路径见页面「接入 Trae SOLO」）
docs/                 文档：用户文档在根（功能说明、使用指南），设计文档在 design/
screenshots/          真实数据下的界面截图
```

## 已实现

- **供应商与模型**：预置类型（KmAiModelHub、OpenCode Go、DeepSeek、OpenAI、Anthropic、自定义）默认地址与协议已对齐官方文档；供应商 ID 可在界面配置并作为请求名前缀（`供应商ID/模型ID`，兼容按名称匹配）；连接测试、模型调用测试、从上游拉取模型并一键导入；上下文窗口与推理档位自动补全（models.dev），不猜命名空间、失败降级。
- **上游兼容**：自定义 `User-Agent`、会话头透传（`x-opencode-session`，兼容 Codex 的 `session_id`）与兜底稳定会话 ID；**供应商多协议（有序多选）+ 原生直通优先**——入口协议命中供应商声明的协议列表时直接直通，未命中才转换；模型可用 `protocol` 固定单协议（如 OpenCode Go 的多端点模型）。
- **本机代理**：Chat / Responses / Messages 三类入口、需求 6.5 的确定性映射、上游认证注入、流式逐事件转发、错误体转换；记录区分直通/转换与转换告警。
- **协议转换**：直通 + Responses↔Chat + Chat→Messages + Responses→Messages（含流式事件）；并行工具调用合并成组、工具结果配对与兜底、思考内容（`reasoning_content`）回填、自定义（自由文本）工具桥接。
- **Codex 配置**：一键接入、自动同步（默认开启，模型增删/默认模型/端口变更触发）、写入前校验与原子替换、断开接入只移除自己写入的配置。
- **OpenCode 配置**：只维护 `llm-switch` 一个 provider 段（`opencode.json`），模型键为 `供应商ID/模型ID`（保证代理侧确定性映射、同名模型不歧义）；**形状自适应**——自动跟随现有文件，或在界面强制 V1（OpenCode 1.x 兼容）/ V2（OpenCode 2.x 原生，额外写入 `modelID` 与 `limit.context`）；一键接入、自动同步（模型增删/端口变更触发，且只在已接入时维护，不会自行创建配置文件）、写入前校验与原子替换、断开接入只移除自己写入的配置；切换形状时自动清理另一种形状下的同名残留；支持 `opencode.jsonc` 场景的明确拒绝（不丢注释）与 `OPENCODE_CONFIG` / `XDG_CONFIG_HOME` 路径解析。
- **Trae SOLO 接入**：页面「一键接入」把 Trae 的自定义模型与本地代理**完全对齐**——模型 ID 即 `供应商ID/模型ID`、请求地址指向本机代理，缺少的补齐、不一致的移除，并在添加时按模型元数据填好 Trae 的**高级配置**（上下文窗口、思考模式、图片输入），结尾复核并如实报告；另有可选的「重新排序」按供应商顺序重建列表（Trae 是「新加排最前」，重建后才能得到稳定顺序，代价是每个模型重跑一次连通性测试）。后端用 Go 内置 CDP 驱动 Trae 自己的设置界面完成注册（对应应用内协议 `chat/add_custom_model`，由服务端注册），**不改 Trae 的数据库与配置**；调试端口通过官方支持的 `argv.json` 写入，Trae 未运行则自动带参数拉起，在运行则引导重启。详见 [Trae-SOLO-接入分析与方案](docs/design/Trae-SOLO-接入分析与方案.md)。
- **默认值**：首个模型自动成为默认模型；开机启动、启动打开面板、Codex/OpenCode 自动同步默认开启；模型元数据自动打「自动」标签。
- **运行形态**：系统托盘、开机启动、单实例、日志滚动、管理页令牌与 Host 校验。

## 验证情况

- `go test ./...`：映射规则、协议转换、代理端到端（httptest 模拟上游）、上游请求头（会话透传/UA）、模型级协议覆盖、Codex 配置合并与生成、OpenCode 配置合并/默认模型回退/断开接入/jsonc 拒绝、配置存储并发与失败回滚、配置自愈。
- 真实 Codex（0.147）：生成的 config.toml / auth.json / 模型清单可被 `codex debug models` 正常解析，`display_name` 与 `slug` 均为 `供应商ID/模型ID`。
- 真实 OpenCode（npm 1.2.22 / 1.2.25 与桌面版 2.0.6）：写入的 provider 段可被 `opencode debug config` 正常加载，`opencode models` 能列出全部 `llm-switch/*`，`opencode run --model llm-switch/<供应商ID>/<模型ID>` 经本机代理完成真实调用（chat 入口 → responses 上游），且配置改动无需重启即生效。两种形状分别验证：V1 形状两个版本都能读；V2 原生形状仅 2.x 能读（1.x 遇到未知键会整份配置报错），因此默认 auto 跟随现有文件。
- 真实链路：模拟上游 → 代理（Responses 请求转换 Chat 流式回传）→ Codex 配置写入 → 界面全流程（截图见 `screenshots/`）。
- 真实上游探测：KmAiModelHub API 路径与 OpenCode Go 三类端点（chat / messages / responses）均验证可达。
- 真实上游复现与修复验证（2026-09-22）：用 Codex 真实失败会话（一轮两个并行 `exec_command` 调用）复现 DeepSeek / OpenCode Go 的 400；修复后同一历史分别验证「合并工具调用」通过、缺 `reasoning_content` 仍 400、补回填后 200。

## 待验证与后续

- Codex 桌面端模型切换菜单（`additional_speed_tiers` 等字段，见模型元数据来源分析第 6 节）。
- 同机多 Agent（Codex / OpenCode）共用代理时的来源识别目前基于 User-Agent 粗判。
- 真实供应商账号下的端到端调用（各供应商协议差异）。
- 管理面令牌目前通过页面注入；如需更高安全性可改为独立登录。
- Trae SOLO 目前只做「手动一键接入/断开」，不做自动同步；`tools/trae-solo/` 下的 Python 脚本保留作为排错参照。
