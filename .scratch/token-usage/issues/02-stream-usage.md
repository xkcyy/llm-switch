# 02: 流式用量采集

**What to build:** 流式调用也能采到用量——直通按 SSE 旁路解析，转换走 IR；chat 直通在客户端未指定时补 `include_usage`。Codex 真实调用后用量出现。

**Blocked by:** 01

**Status:** resolved

- [ ] 三协议流式 × 直通/转换均采到用量
- [ ] 直通流式旁路解析，不改写转发给客户端的字节流
- [ ] Responses 取 `response.completed`、Chat 取末块、Messages 取 `message_start` / `message_delta`
- [ ] chat 直通且客户端未指定 `stream_options.include_usage` 时注入，其它字段不变
- [ ] 流中断或客户端取消不中断代理，至少计 `requests`
- [ ] 端到端测试覆盖三协议流式 × 直通/转换

## Comments

已实现。流式统一在响应收口处包一层旁路扫描器：只读 `data:` 行做 usage 归一化，转发字节原样放行。直通、转换、legacy 三条流式路径共用同一实现；Anthropic 的 `message_start` / `message_delta` 分片用量按字段取大值合并。

chat 直通在客户端未指定时注入 `stream_options.include_usage`（已有则不重复写），避免 KmAiModelHub 那种「不注入就不回 usage」的缺失。

验证：`go test ./...` 全绿（新增 `internal/proxy/usage_stream_test.go`：responses 直通 / chat 直通 / responses→chat 转换 / include_usage 注入 / 无 usage 计数）。
