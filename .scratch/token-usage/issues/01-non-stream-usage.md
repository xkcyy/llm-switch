# 01: 非流式用量采集与落桶

**What to build:** 代理服务非流式调用时采集上游上报的用量（调用次数与 token），按小时与日落桶，并通过只读接口给出汇总；总览页能看到今日 token。

**Blocked by:** None (can start immediately)

**Status:** resolved

- [ ] chat / responses / messages 三种协议的非流式调用，无论直通还是转换，都采到输入、输出、合计 token 与调用次数
- [ ] 上游未上报 usage 时 token 记 0，调用计入 `missing_usage`，不估算
- [ ] usage 字段名可归一化：`input_tokens` 与 `prompt_tokens` 两种形状都能解析
- [ ] 分桶键为 供应商ID × 模型ID × 调用方；不同供应商下的同名模型不混淆
- [ ] 落桶同时写小时桶与日桶（内存）
- [ ] `GET /api/usage` 返回总量（requests / ok / err / token 各项）
- [ ] 总览页显示今日 token 合计
- [ ] 端到端测试：httptest 模拟上游，覆盖三协议非流式 × 直通/转换

## Comments

已实现。采集收口在 `internal/proxy/protocol` 的 `Deliver`，非流式三个分支（直通 / 转换 / legacy）都从原始响应体解析 usage；`internal/usage` 按 供应商 × 模型 × 调用方 落小时桶与日桶；`GET /api/usage` 返回累计与今日；总览页新增今日调用与 token 卡片。

字段归一化覆盖 `input_tokens`/`prompt_tokens` 与缓存、推理字段的多种位置；上游不回 usage 记 `missing_usage`，失败调用计入 `err` 且不算 missing。

验证：`go test ./...` 全绿（新增 `internal/proxy/usage_test.go`、`internal/proxy/protocol/usage_test.go`、`internal/usage/usage_test.go`）；`web` 构建通过。
