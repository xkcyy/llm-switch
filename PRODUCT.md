# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Vite 5 + React 18 + Ant Design 5 in `web/`; Go backend embeds the production build into a single Windows EXE.

## Users

Windows 本机开发者，通常已安装 Codex、OpenCode、Trae SOLO、DeepSeek Harness 或 WorkBuddy。用户在配置多个模型供应商、排查模型调用问题，或需要让多个 Agent 共用同一组模型时使用本产品。

## Product Purpose

LLM Switch 统一管理多个模型供应商，通过仅监听本机的代理向多个 Agent 提供统一模型入口，并自动维护它们的接入配置。成功意味着用户选择预设、粘贴 API Key 后即可使用，无需理解协议转换、路由或配置文件细节。

## Positioning

产品把供应商、可用模型、本机代理和多个 Agent 的接入配置放在一个本机控制台中维护。模型以 `供应商ID/模型ID` 唯一标识；供应商协议与 Agent 入口协议可以不同，代理优先直通、必要时转换。接入配置只在模型、默认模型或代理端口变化时自动同步。

## Operating Context

- Windows 桌面环境；管理面运行在 `127.0.0.1:8318`，模型代理默认运行在 `127.0.0.1:8317`。
- 配置目录默认为 `%USERPROFILE%\.llm-switch\`，由本机用户单独使用。
- 常见任务包括新增供应商、连接测试、拉取并导入模型、测试模型调用、接入 Agent、查看用量与请求轨迹。
- 生产页面由 Go 服务端注入管理令牌；前端只通过 `/api/*` 与后端通信。
- 当前导航包含总览、供应商与模型、5 个接入页、用量、请求轨迹和设置。

## Capabilities and Constraints

- 支持预置与自定义供应商，多协议声明，连接测试、模型拉取、模型测试与能力元数据补全。
- 支持 Codex、OpenCode、Trae SOLO、DeepSeek Harness、WorkBuddy 接入；各接入页写入并维护不同配置文件。
- 供应商认证信息保存在本机配置中，界面默认脱敏；完整值不得出现在日志、轨迹或 Agent 配置中。
- 代理支持 OpenAI Chat、OpenAI Responses、Anthropic Messages 三类入口；同协议直通，跨协议经 Responses 中间表示转换。
- 用量仅采信上游上报；缺失用量必须显式标注，不得估算。
- 保留 Ant Design 5.22.x 组件底座，不升级组件库，不在本次改造中改动 `/api/*` 契约、`X-Admin-Token`、Go 转发与配置写入行为。
- 术语以 `CONTEXT.md` 为准，特别是供应商、模型、可用模型、默认模型、协议、直通、转换、中间表示、思考内容、兼容扩展、接入、会话、调用方、用量。
- 本次改造保留全部功能、文案事实、行为与术语，替换视觉系统并重整信息架构。
- 不得为先视觉效果增加用户步骤、字段或确认。

## Brand Commitments

- 产品名：LLM Switch。
- 核心承诺：默认即正确、用户零思考。
- 产品语气直接、克制、以可执行结果为中心；不把技术实现细节变成用户负担。

## Evidence on Hand

- `README.md`：功能、运行方式、验证情况与产品承诺。
- `CONTEXT.md`：领域术语与禁用同义词。
- `docs/design/LLM-Switch-产品需求与功能规划.md`：用户、核心场景、信息架构、核心流程与验收标准。
- `docs/adr/0001-responses-as-ir.md`、`docs/adr/0002-usage-from-upstream-only.md`：不可误解的领域决策。
- `screenshots/`：现有真实数据界面截图，可作为改造前证据。

## Product Principles

1. 默认即正确：预设、自动补全和自动同步应消除不必要的选择。
2. 先完成主任务：配置与接入流程优先于观察和诊断信息。
3. 状态必须可解释：失败要指出问题与恢复动作，缺失数据不伪造。
4. 一致性高于装饰：同类对象、同类操作和同类状态使用同一套表达。
5. 本机、单人、可诊断：界面为桌面高频操作优化，同时保留深链和键盘可达性。
