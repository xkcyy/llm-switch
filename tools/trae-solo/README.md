# Trae SOLO 模型同步（脚本版）

> 说明：产品里已经内置了页面版「一键接入」（`internal/trae`，Go 直接说 CDP）。
> 本目录的 Python 脚本保留作为**排错参照**：逻辑一致、可独立运行、方便对照界面行为。

把 LLM Switch 的模型批量注册到 **TRAE SOLO CN** 的自定义模型列表。

- 原理与实测结论：[docs/design/Trae-SOLO-接入分析与方案.md](../../docs/design/Trae-SOLO-接入分析与方案.md)
- 脚本：`sync_models.py`

## 快速开始

```powershell
pip install playwright          # 只需这个；连的是已有 Electron，不用 playwright install

# 1) 开启调试端口（写入 %USERPROFILE%\.trae-cn\argv.json，自动备份）
python sync_models.py --configure-argv
#    然后重启 Trae（或 --restart 让脚本代劳，会中断正在跑的会话）

# 2) 看看会做什么
python sync_models.py --dry-run

# 3) 同步
python sync_models.py
```

## 参数

| 参数 | 说明 |
|---|---|
| `--api` | LLM Switch 的 OpenAI 兼容入口（默认按 `~/.llm-switch/config.json` 推导，通常 `http://127.0.0.1:8317/v1`） |
| `--url` | 填进 Trae 的「自定义请求地址」（默认 `--api`，**不要带** `/chat/completions`） |
| `--api-key` | 填进 Trae 的 API Key，代理不校验（默认 `llm-switch-local`） |
| `--display` | 显示名模板，`{id}` 替换为模型 ID（默认与模型 ID 相同） |
| `--only` | 只处理指定模型 ID，可重复 |
| `--dry-run` | 只对比，不改 Trae |
| `--configure-argv` | 端口未开时写入 argv.json（需手动重启） |
| `--restart` | 端口未开时自动重启 Trae |
| `--timeout` | 单个模型的连通性测试等待上限（秒，默认 300） |
| `--on-test-fail` | `save`（默认，点「直接保存」仍然注册）/ `skip` |
| `--shots-dir` | 失败截图目录 |

## 已知行为

- 连通性测试会发**图片输入**，纯文本模型必然测试失败；此时脚本默认点「直接保存」注册。
- 上游慢（实测 KmAiModelHub 单次 25s~400s+）时会等较久，必要时 `--timeout` 调大。
- 重复执行安全：已存在的模型按模型 ID 跳过。
- 卸载：在 Trae「设置 → 模型」里删除对应条目；`argv.json` 的调试端口可手删（备份在 `argv.json.bak-llmswitch`）。

## 与页面版的差异

页面上的一键接入（`internal/trae`，Go 内置 CDP）比本脚本多做了三件事，脚本仅作排错参照：

1. **会填「高级配置」**：上下文窗口（输入/输出）、思考模式、图片输入；
2. **图片输入会自动纠正**：测试报「不支持图片输入」时改成「不支持」并重试，测试因此通过；
3. **双向对齐 + 复核**：补齐缺失、删除不一致，并在结尾重新读一遍界面给出结论。
