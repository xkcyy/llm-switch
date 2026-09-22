# Trae SOLO 接入分析与方案

| 项目 | 内容 |
|---|---|
| 结论 | **可行，且已跑通全量并产品化**。16 个模型已全部注册进 Trae SOLO 自定义模型列表，页面里可一键接入/断开 |
| 验证环境 | TRAE SOLO CN 1.107.1（VS Code 1.107.1 / Electron 39.2.7），Windows，2026-09-22 |
| 交付物 | 页面「接入 Trae SOLO」（`internal/trae` + `internal/cdp` + `web/src/pages/Trae.jsx`）；调试脚本 [tools/trae-solo/sync_models.py](../../tools/trae-solo/sync_models.py) |
| 相关证据 | 应用内协议方法名、`product.json`、`state.vscdb` 缓存、代理日志、UI 截图（见下文） |

---

## 1. 结论

「用 CDP + Playwright 驱动 Trae 的官方 UI 添加自定义模型」这个思路**成立**，而且比原方案设想的更顺：

- Trae SOLO CN 是 VS Code 分支（`resources/app/product.json`：`nameLong` = `TRAE SOLO CN`，`applicationName` = `trae-solo-cn`，VS Code `1.107.1`，Electron `39.2.7`），Electron 原生支持 `--remote-debugging-port`；
- 更关键的是 `remote-debugging-port` 在 **argv.json 白名单**里（`out/main.js` 中的 `["disable-hardware-acceleration","force-color-profile","disable-lcd-text","proxy-bypass-list","remote-debugging-port"]`），也就是说**可以永久开启调试端口**，不需要每次换快捷方式启动；
- 添加自定义模型走的是应用内协议请求 `chat/add_custom_model`（`@byted-icube/ai-modules-chat` 里可见同名 `ProtocolRequestType`），与 `chat/update_custom_model`、`chat/test_custom_model_connection` 并列，**由服务端注册并返回 `custom_model_id`**，本地只留缓存；
- 自定义模型 `client_connect = true`，官方文案明确「只支持在 TRAE 本地环境使用，暂不支持云端环境」——正好适配 `127.0.0.1` 的本机代理。

真机验证的三条硬证据：

1. **请求确实发出去了**：代理日志出现
   `entry=chat source=… model=ds/deepseek-flash provider=DeepSeek target=deepseek-flash upstream=chat status=200 ms=1590`
2. **`model` 字段就是「模型 ID」**：这条测试的显示名故意写成 `LS-test-deepseek-flash`，日志里出现的是 `ds/deepseek-flash`，说明显示名不参与请求；
3. **UI 上确实成功了**：Trae 弹出「测试通过，模型保存成功」，模型管理表格里出现新行；用 UI 删除后再次查询确认已移除。

最终结果：脚本连续跑完，16 个模型全部出现在「设置 → 模型」列表（服务商显示为「自定义(OpenAI Compatible)」），
再跑 `--dry-run` 输出「全部已存在，无需处理」。其中多数模型的连通性测试因「不支持图片输入」未通过，
由脚本点官方「直接保存」完成注册——功能与手点完全等价。剩余少数上游本身返回 4xx/5xx 的模型
（如 `ocg/hy4-preview` 404、`ocg/grok-4.6` 503）注册成功但调用会失败，需要在 LLM Switch 里先修好上游。

---

## 2. 实际流程（与原方案的差异）

原方案的骨架正确，但有几处会直接踩坑，按重要程度排列：

| # | 原方案 | 实际情况 | 修正 |
|---|---|---|---|
| 1 | 地址填 `http://127.0.0.1:8317/v1/chat/completions` | 「完整 URL」开关默认**关闭**，Trae 会在你填的地址后**再补一次** `/chat/completions` | 填 `http://127.0.0.1:8317/v1`（推荐）；或打开「完整 URL」开关后用带 `/chat/completions` 的地址 |
| 2 | 「进入 设置 → 模型 → 添加自定义模型」 | 实际是 4 步：模型选择器（`button.core-model-select-trigger`）→ 弹层里「添加模型」→ 设置/模型页「+ 添加模型」→ 供应商列表选「自定义模型」 | 脚本按这条路径走；设置入口不止底部齿轮一个 |
| 3 | 提交后「等成功提示再进下一条」 | 成功提示（Toast）会残留数秒，**下一条会读到上一条的提示**，导致在「连通性测试中」的 disabled 按钮上点击而失败 | 用**弹窗关闭**作为成功信号；提交前先等上一次测试结束、必要时点「重置」清空表单 |
| 4 | 16 条硬编码 | 模型清单会变 | 从 `GET http://127.0.0.1:8317/v1/models` 动态取（正好返回 `供应商ID/模型ID`） |
| 5 | 已存在则跳过 | 判断依据要看**模型 ID**，不是显示名 | 读 `state.vscdb` 的 `AI.agent.model.model_list_map` 取已注册的模型 ID（本地缓存，只读）；UI 端「模型已存在」是第二道校验 |
| 6 | 锁定标题为 `TraeWork CN` 的页面 | ✅ 正确（页面是 `.../electron-browser/solo/solo-lite.html`） | 建议标题 + URL 双条件，标题会跟随语言/版本 |
| 7 | 校验：日志出现 `entry=chat model=<ID> status=200` | ✅ 正确，已实测 | 保留，作为端到端验收手段 |

补充两个实测发现：

- **连通性测试会发图片输入**。纯文本 / 不支持视觉的模型必然测试失败，报
  `Upstream request failed: [invalid_request_error] This model does not support image inputs (HTTP Status: 400)`。
  此时对话框会给出**「直接保存」**按钮，仍然可以注册。脚本默认走「直接保存」（`--on-test-fail save`）。
- **上游慢会导致测试很久**。`KmAiModelHub` 走 responses 上游，实测单次 25s ~ 400s+；脚本超时默认 300s 可调（`--timeout`）。

---

## 3. 落地：`tools/trae-solo/sync_models.py`

```powershell
# 1) 让 Trae 常驻调试端口（写入 %USERPROFILE%\.trae-cn\argv.json，备份原文件）
python tools\trae-solo\sync_models.py --configure-argv
#    然后手动重启 Trae（或加 --restart 让脚本代劳，会中断正在跑的会话）

# 2) 先看差异，不动 Trae
python tools\trae-solo\sync_models.py --dry-run

# 3) 同步（缺什么补什么，已存在跳过）
python tools\trae-solo\sync_models.py

# 常用参数
python tools\trae-solo\sync_models.py --only km/gpt-5.6-sol      # 只处理指定模型
python tools\trae-solo\sync_models.py --display "{id}[ls]"       # 自定义显示名模板
python tools\trae-solo\sync_models.py --on-test-fail skip        # 测试不过就跳过，不「直接保存」
python tools\trae-solo\sync_models.py --shots-dir .\shots        # 失败时留截图
```

脚本行为要点：

- 只依赖 `playwright`（`pip install playwright`），**不需要** `playwright install`（连的是已有 Electron，不下载浏览器）；
- 幂等：先按模型 ID 去重，已存在的直接跳过；UI 端报「模型已存在」也会算跳过；
- 结果分三类：`连通性测试通过`、`测试失败后直接保存`、`失败`；失败会留截图并可重跑；
- 不改数据库、不改本地配置、不写日志文件；所有写入都由 Trae 自己的 UI 流程完成。

---

## 4. 机理说明（为什么这条路最稳）

| 层面 | 说明 |
|---|---|
| 为什么能用 CDP | Electron 应用；`argv.json` 的 `remote-debugging-port` 进入 `app.commandLine.appendSwitch`，端口只监听本机 |
| 为什么点得到 Radix 组件 | 模型选择器等是 Radix，只认 pointer 事件，合成 `element.click()` 无效；脚本用真实鼠标事件（CDP `Input`） |
| 为什么不用写配置 | 自定义模型由服务端注册（返回 `custom_model_id`），本地 `state.vscdb` 只是缓存；直接写库会被同步覆盖，且 `ak` 是加密存储 |
| 为什么选 UI 而不是直接调接口 | `chat/add_custom_model` 是应用内 iCube 协议请求，经主进程转到 `wss://…/custom_model`，带 protobuf 与 JWT；复刻成本高、随版本失效。UI 路径是官方入口，等价于手点 |
| 稳定选择器 | `button.core-model-select-trigger`、`button.add-model-connect-button`、`button.add-model-reset-button`、`input[placeholder*=模型 ID]`、`codicon-icube-Delete2` 等均为非哈希类名；同时保留了中英文占位符兜底 |

---

## 5. 风险与回滚

| 风险 | 影响 | 应对 |
|---|---|---|
| 调试端口常开 | 本机任意进程可控制 IDE | 只监听 127.0.0.1；不需要时删掉 `argv.json` 里的键并重启（原文件已备份为 `argv.json.bak-llmswitch`） |
| UI 结构随版本变化 | 脚本失效 | 选择器集中写在文件头部常量，失败有截图；真机升级后按截图微调 |
| 连通性测试消耗 Token | 每次一条极小请求 | 上游返回 400/503 等会被 Trae 拦下并给「直接保存」，不重复打 |
| 上游本身不可用 | 模型注册成功但调用报错 | 脚本报告具体 HTTP 状态；先在 LLM Switch 里用「模型测试」确认，或换协议 |
| 服务端注册 | 模型列表跟账号走，会同步到其它设备 | 官方行为；删除同样走 UI（脚本流程里已包含删除按钮的选择器） |
| 慢上游 | 测试超时被判失败 | `--timeout` 调大；或先给该供应商调整协议/上游 |

回滚：在 Trae「设置 → 模型」里删除对应条目即可（删除是官方接口，本地缓存会跟随更新）。

---

## 6. 已实现：LLM Switch 页面里的「一键接入」

按 Codex / OpenCode 的既有模式，产品里加了「接入 Trae SOLO」页（不做自动同步）：

| 能力 | 说明 |
|---|---|
| 一键接入 | 内部完成「准备调试端口 → 读取 Trae 现有模型 → 算差异 → 补齐缺失 + 删除不一致 + 复核」，用户只看到进度与结果 |
| 高级配置 | 添加模型时把 LLM Switch 的元数据写进 Trae 的「高级配置」：上下文窗口（输入/输出）、思考模式、图片输入 |
| 重新排序 | 可选动作：删掉全部代理模型后按配置顺序重建（同供应商相邻）。Trae 的列表是「新加的排最前」，所以重建时按逆序添加；因为每个模型都要重跑连通性测试，可能要几分钟 |
| 状态卡片 | Trae 位置、调试端口状态、请求地址、代理模型数 |
| 需要重启时 | Trae 正在运行但没开调试端口 → 页面提示并提供「重启 Trae 并继续」 |
| 进度与结果 | 任务态（准备/读取/写入/复核）+ 逐模型结果（已存在 / 已添加 / 测试未通过但已保存 / 已删除 / 失败），结尾给一句如实结论 |

### 高级配置怎么填

Trae 的「高级配置」里有：模型系列、上下文窗口（输入 / 输出）、工具调用轮数、支持图片输入、思考模式、采样参数。映射关系：

| Trae 字段 | 来源 | 说明 |
|---|---|---|
| 上下文窗口 · 输入 | `models[].context_window` | models.dev 自动补全，实测 15/16 个模型有值 |
| 上下文窗口 · 输出 | `models[].max_output_tokens` | 目前上游元数据多为空，留空即用 Trae 默认 |
| 思考模式 | `models[].reasoning.levels` 非空 → 开启 | 没有档位信息时不设置（保持「跟随模型默认配置」） |
| 支持图片输入 | 先按能力标注，未知则**用 Trae 的连通性测试反推** | 见下 |
| 模型系列 / 工具调用轮数 / 采样参数 | 不设置 | 保持 Trae 默认（500 轮、最佳采样） |

**图片输入的反推**：Trae 的连通性测试会发一次带图片的真实请求，纯文本模型必然报
`This model does not support image inputs`。此时自动化会把「支持图片输入」改成**不支持**并**再试一次**——
改完之后测试会跳过图片、正常通过，模型也就带着正确的能力被注册；实测 `ocg/glm-5.3` 就是这样从
「测试失败 → 直接保存」变成「测试通过」的。

存储侧核对（Trae 本地缓存 `state.vscdb`）：`prompt_max_tokens` = 上下文窗口输入、
`thinking_enable` = 思考模式、`multimodal` = 图片输入，三者都会随高级配置一起写入。

实现要点：

- **后端 Go 内置 CDP 驱动**（`internal/cdp` + `internal/trae`），只新增 `github.com/gorilla/websocket` 一个依赖，保持「单文件 EXE、无外部依赖」；
- **差异来源是 Trae 自己的设置页表格**（服务端真值）。实测 `state.vscdb` 字节扫描会出现 11 个假阳性（已删除模型的残留），纯 Go 读 SQLite 又要引入重型依赖，因此选择「点按钮时内部扫一次」——约 4 秒，且与写入口径完全一致；
- **扫描必须等表格就绪**：Trae 先渲染页面壳、表格数据后到；读早了会得到 0 行，导致「明明有模型却判定为没有」——同步会重复添加、删除会整批漏掉。实现上要求「连续两次读到的行数一致」才采信；
- **填表用 JS 原生 setter + input 事件**（已实测能更新 React 状态），只有 Radix 组件的点击必须走 CDP 真实鼠标事件；
- **成功判定用「弹窗关闭」+ 列表回读校验**，不依赖会残留的 Toast；
- **连通性测试带图片输入**，纯文本模型必然失败，脚本/页面都会走官方「直接保存」兜底；
- 排错用：`$env:TRAE_E2E=1; go test ./internal/trae -run E2E -v`（需要 Trae 正在运行；`TRAE_E2E_TARGET=<模型ID>` 可从 Trae 删掉指定模型，`TestE2EDumpRows` 打印表格原始行）。

> 产品化里最容易出问题的不是自动化本身，而是「Trae 没开调试端口」这一步：页面用「自动写入 argv.json + 未运行就带参数拉起 + 在运行则引导重启」三段式处理。
