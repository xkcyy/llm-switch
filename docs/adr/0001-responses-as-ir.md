# 协议转换以 Responses 作为中间表示

转换路径（入口协议 ≠ 上游协议）的中间表示采用 Responses 的条目与事件词汇，以 Go 类型实现，字段集严格等于 Responses 公开字段；同协议直通不经过它。这样每个协议只需一组解码器与编码器，成对转换不再按方向增长，Responses 的条目词汇（`function_call` / `function_call_output` / `custom_tool_call` / `reasoning` 条目）也不必用中立名重写一遍。

## Considered Options

- **中立 IR**（原 §9.1 的 `UnifiedRequest`）：同一件事要用中立名重写一遍，且 IR 一旦中立就与 Responses 的条目词汇并行演化，需要维护两套术语。
- **IR 带扩展槽**：用扩展字段承载各家私有的回传载荷（Chat 的 `reasoning_content`、Anthropic 的 `thinking.signature`），会让 IR 变成「Responses ∪ 各家私有字段」，深度当场消失。

思考内容走 Responses 原生的 `reasoning` 条目：上游给出的推理文本随响应下发，客户端下一轮原样回传，转换时据此回填上游要求的 `reasoning_content`。进程内缓存（键：供应商 ID + 模型 ID + 工具调用 ID）与占位文本只是兜底，用于客户端未回传或跨进程的窗口期，不作为 IR 字段。
