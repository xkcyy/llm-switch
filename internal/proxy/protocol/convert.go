// Package protocol 实现 OpenAI Chat、OpenAI Responses、Anthropic Messages
// 三种协议之间的请求、响应与流式事件转换。
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Protocol string

const (
	Chat      Protocol = "chat"
	Responses Protocol = "responses"
	Messages  Protocol = "messages"
)

func Parse(s string) (Protocol, error) {
	switch Protocol(s) {
	case Chat, Responses, Messages:
		return Protocol(s), nil
	default:
		return "", fmt.Errorf("未知协议 %q", s)
	}
}

// Path 返回该协议在代理上的入口路径（相对根）。
func (p Protocol) Path() string {
	switch p {
	case Chat:
		return "v1/chat/completions"
	case Responses:
		return "responses"
	case Messages:
		return "messages"
	}
	return ""
}

// UpstreamPath 返回相对 base_url 的请求路径（base_url 约定包含 /v1）。
func (p Protocol) UpstreamPath() string {
	switch p {
	case Chat:
		return "chat/completions"
	case Responses:
		return "responses"
	case Messages:
		return "messages"
	}
	return ""
}

// ---- 通用 JSON 辅助 ----

type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// textOf 从 string 或内容块数组提取纯文本。
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []textPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func marshal(v any) ([]byte, error) { return json.Marshal(v) }

// CanConvert 判断「入口协议 → 上游协议」是否存在可用的转换实现。
func CanConvert(entry, up Protocol) bool {
	if entry == up {
		return true
	}
	switch {
	case entry == Responses && up == Chat:
		return true
	case entry == Responses && up == Messages:
		return true
	case entry == Chat && up == Messages:
		return true
	case entry == Chat && up == Responses:
		return true
	}
	return false
}

// ChatConvertOptions 控制 Responses → Chat 的转换细节。
type ChatConvertOptions struct {
	// Reasoning 返回某个工具调用对应的上游思考内容（DeepSeek 等思考模式要求回传）。
	// 返回 ok=false 时不写入该字段。
	Reasoning func(callID string) (string, bool)
	// OnWarning 上报转换中的降级行为（如丢弃孤立工具结果），供日志与界面提示。
	OnWarning func(string)
}

func (o ChatConvertOptions) warn(format string, args ...any) {
	if o.OnWarning != nil {
		o.OnWarning(fmt.Sprintf(format, args...))
	}
}

// ResponseConvertOptions 控制「上游 → 入口」的响应转换细节。
type ResponseConvertOptions struct {
	// CustomTools 是本次请求中声明为 custom（自由文本）的工具名集合；
	// 上游返回同名工具调用时按 custom_tool_call 下发，否则客户端无法识别。
	CustomTools map[string]bool
	// OnReasoning 在拿到上游思考内容时回调（推理文本 + 本次响应涉及的 call_id 列表）。
	OnReasoning func(reasoning string, callIDs []string)
	// OnWarning 上报降级行为。
	OnWarning func(string)
}

func (o ResponseConvertOptions) warn(format string, args ...any) {
	if o.OnWarning != nil {
		o.OnWarning(fmt.Sprintf(format, args...))
	}
}

// CustomToolNames 提取请求里声明为 custom（自由文本）的工具名。
func CustomToolNames(body []byte) map[string]bool {
	req, _, err := parseResponsesRequest(body)
	if err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, t := range req.Tools {
		if t.Type == "custom" && t.Name != "" {
			names[t.Name] = true
		}
	}
	return names
}

// freeformArguments 把自由文本包装成上游函数调用参数。
func freeformArguments(input string) string {
	b, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// freeformInput 还原自由文本：模型返回 {"input": "..."} 这类包装时取出内容，否则原样使用。
func freeformInput(args string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &m) == nil {
		for _, k := range []string{"input", "patch", "code", "text"} {
			raw, ok := m[k]
			if !ok {
				continue
			}
			var s string
			if json.Unmarshal(raw, &s) == nil {
				return s
			}
		}
	}
	return args
}

// ---- 请求转换：Responses → Chat ----

type responsesRequest struct {
	Model             string              `json:"model"`
	Instructions      json.RawMessage     `json:"instructions,omitempty"`
	Input             json.RawMessage     `json:"input"`
	Tools             []responsesTool     `json:"tools,omitempty"`
	ToolChoice        json.RawMessage     `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool               `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens   *int                `json:"max_output_tokens,omitempty"`
	Temperature       *float64            `json:"temperature,omitempty"`
	TopP              *float64            `json:"top_p,omitempty"`
	Stream            bool                `json:"stream,omitempty"`
	Reasoning         *responsesReasoning `json:"reasoning,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
	Input     string          `json:"input"` // 自定义（自由文本）工具调用的输入
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	// ReasoningContent 是思考型上游（DeepSeek 等）要求回传的推理内容；
	// 指针类型用于区分「不写该字段」与「写空字符串」。
	ReasoningContent *string `json:"reasoning_content,omitempty"`
	// Reasoning 是部分网关（OpenRouter 风格）的推理字段别名，仅用于解析上游响应。
	Reasoning string `json:"reasoning,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type chatRequest struct {
	Model             string         `json:"model"`
	Messages          []chatMessage  `json:"messages"`
	Tools             []chatTool     `json:"tools,omitempty"`
	ToolChoice        any            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	MaxTokens         *int           `json:"max_tokens,omitempty"`
	Temperature       *float64       `json:"temperature,omitempty"`
	TopP              *float64       `json:"top_p,omitempty"`
	Stream            bool           `json:"stream,omitempty"`
	StreamOptions     *streamOptions `json:"stream_options,omitempty"`
	ReasoningEffort   string         `json:"reasoning_effort,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func parseResponsesRequest(body []byte) (*responsesRequest, []responsesInputItem, error) {
	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, fmt.Errorf("解析 Responses 请求失败: %w", err)
	}
	var items []responsesInputItem
	if len(req.Input) > 0 && string(req.Input) != "null" {
		if err := json.Unmarshal(req.Input, &items); err != nil {
			// input 为纯字符串
			var s string
			if err2 := json.Unmarshal(req.Input, &s); err2 == nil {
				items = []responsesInputItem{{Type: "message", Role: "user", Content: req.Input}}
			} else {
				return nil, nil, fmt.Errorf("解析 input 失败: %w", err)
			}
		}
	}
	return &req, items, nil
}

func responsesToolsToChat(tools []responsesTool, opts ChatConvertOptions) []chatTool {
	out := make([]chatTool, 0, len(tools))
	for _, t := range tools {
		switch t.Type {
		case "", "function":
			ct := chatTool{Type: "function"}
			ct.Function.Name = t.Name
			ct.Function.Description = t.Description
			ct.Function.Parameters = t.Parameters
			out = append(out, ct)
		case "custom":
			// 自定义（自由文本）工具降级为「单个 input 字符串」的函数工具，
			// 否则上游模型看不到工具定义，历史里的调用与结果也会被丢弃。
			ct := chatTool{Type: "function"}
			ct.Function.Name = t.Name
			desc := t.Description
			if desc == "" {
				desc = "Freeform tool."
			}
			ct.Function.Description = desc + " Provide the full content in the `input` string field."
			ct.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"Full freeform input (raw text: script or patch)."}},"required":["input"]}`)
			out = append(out, ct)
		default:
			opts.warn("忽略不支持的工具类型 %q（%s）", t.Type, t.Name)
		}
	}
	return out
}

func responsesToolChoiceToChat(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.Name != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": obj.Name}}
		}
		if obj.Type != "" {
			return obj.Type
		}
	}
	return nil
}

// ResponsesToChatRequest 把 Responses 请求转换为 Chat Completions 请求。
func ResponsesToChatRequest(body []byte, model string) ([]byte, error) {
	return ResponsesToChatRequestWith(body, model, ChatConvertOptions{})
}

// ResponsesToChatRequestWith 在转换时支持思考内容回填与降级告警。
//
// Chat Completions 要求：带 tool_calls 的 assistant 消息必须被对应 tool_call_id 的
// tool 消息立即跟随。Codex 的一轮对话可能包含多个并行的工具调用，因此这里把连续的
// 工具调用合并到同一条 assistant 消息，并让它与工具结果成组输出；孤立的结果条目会被
// 丢弃并告警，避免上游 400。
func ResponsesToChatRequestWith(body []byte, model string, opts ChatConvertOptions) ([]byte, error) {
	req, items, err := parseResponsesRequest(body)
	if err != nil {
		return nil, err
	}
	out := chatRequest{
		Model:             model,
		Tools:             responsesToolsToChat(req.Tools, opts),
		ToolChoice:        responsesToolChoiceToChat(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
		MaxTokens:         req.MaxOutputTokens,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		Stream:            req.Stream,
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" && req.Reasoning.Effort != "none" {
		out.ReasoningEffort = req.Reasoning.Effort
	}
	if out.Stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if s := textOf(req.Instructions); s != "" {
		out.Messages = append(out.Messages, chatMessage{Role: "system", Content: s})
	}

	// 工具结果索引：call_id → 结果文本；缺少 call_id 的按出现顺序排队，
	// 供同样缺少 call_id 的工具调用按顺序配对。
	results := map[string]string{}
	var emptyIDResults []string
	for _, it := range items {
		switch it.Type {
		case "function_call_output", "custom_tool_call_output":
			if it.CallID == "" {
				emptyIDResults = append(emptyIDResults, textOf(it.Output))
			} else if _, ok := results[it.CallID]; !ok {
				results[it.CallID] = textOf(it.Output)
			}
		}
	}
	consumed := map[string]bool{}
	emptyConsumed, emptySeen := 0, 0

	isDropped := func(t string) bool {
		switch t {
		case "reasoning", "web_search_call", "local_shell_call", "computer_call",
			"ghost_snapshot", "mcp_call", "mcp_list_tools", "mcp_approval_request",
			"mcp_approval_response", "code_interpreter_call", "image_generation_call":
			return true
		}
		return false
	}

	for i := 0; i < len(items); i++ {
		it := items[i]

		// 工具调用：把紧随其后的连续调用条目合并成一条 assistant 消息。
		if it.Type == "function_call" || it.Type == "custom_tool_call" {
			type callItem struct {
				id      string
				name    string
				args    string
				virtual bool // 原始条目缺少 call_id，为合法成组临时生成
			}
			var calls []callItem
			for i < len(items) {
				cur := items[i]
				switch {
				case cur.Type == "function_call":
					calls = append(calls, callItem{id: cur.CallID, name: cur.Name, args: cur.Arguments})
				case cur.Type == "custom_tool_call":
					calls = append(calls, callItem{id: cur.CallID, name: cur.Name, args: freeformArguments(cur.Input)})
				case isDropped(cur.Type):
					i++
					continue
				default:
					goto groupDone
				}
				i++
			}
		groupDone:
			i--

			msg := chatMessage{Role: "assistant"}
			virtualIDs := map[string]bool{}
			for idx, c := range calls {
				id := c.id
				if id == "" {
					id = fmt.Sprintf("call_%d_%s", idx, c.name)
					c.virtual = true
				}
				if c.virtual {
					virtualIDs[id] = true
				}
				var tc chatToolCall
				tc.ID = id
				tc.Type = "function"
				tc.Function.Name = c.name
				tc.Function.Arguments = c.args
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
			// 思考型上游（DeepSeek 等）要求回传推理内容。
			if opts.Reasoning != nil {
				var fallback *string
				for _, tc := range msg.ToolCalls {
					text, ok := opts.Reasoning(tc.ID)
					if !ok {
						continue
					}
					if text != "" {
						msg.ReasoningContent = &text
						fallback = nil
						break
					}
					if fallback == nil {
						v := text
						fallback = &v
					}
				}
				if msg.ReasoningContent == nil {
					msg.ReasoningContent = fallback
				}
			}
			out.Messages = append(out.Messages, msg)

			// 结果必须紧跟 assistant 消息，缺结果时补占位，避免上游拒绝。
			for _, tc := range msg.ToolCalls {
				text, ok := results[tc.ID]
				switch {
				case ok:
					consumed[tc.ID] = true
				case virtualIDs[tc.ID] && len(emptyIDResults) > emptyConsumed:
					text, ok = emptyIDResults[emptyConsumed], true
					emptyConsumed++
				}
				if !ok {
					opts.warn("工具调用 %s（%s）缺少结果，已补空结果", tc.ID, tc.Function.Name)
					text = "(no output)"
				}
				out.Messages = append(out.Messages, chatMessage{Role: "tool", ToolCallID: tc.ID, Content: text})
			}
			continue
		}

		switch it.Type {
		case "", "message":
			role := it.Role
			if role == "developer" {
				role = "system"
			}
			if role == "" {
				role = "user"
			}
			out.Messages = append(out.Messages, chatMessage{Role: role, Content: textOf(it.Content)})
		case "function_call_output", "custom_tool_call_output":
			if it.CallID != "" {
				if consumed[it.CallID] {
					continue // 已随对应工具调用成组输出
				}
				opts.warn("丢弃没有对应工具调用的结果条目（call_id=%s）", it.CallID)
				continue
			}
			emptySeen++
			if emptySeen <= emptyConsumed {
				continue
			}
			opts.warn("丢弃没有对应工具调用的结果条目（无 call_id）")
		default:
			// reasoning 等条目忽略
		}
	}
	if len(out.Messages) == 0 {
		out.Messages = []chatMessage{{Role: "user", Content: ""}}
	}
	return marshal(out)
}

// ---- 请求转换：Responses → Messages ----

type messagesRequest struct {
	Model       string            `json:"model"`
	System      string            `json:"system,omitempty"`
	Messages    []messagesMessage `json:"messages"`
	Tools       []messagesTool    `json:"tools,omitempty"`
	ToolChoice  any               `json:"tool_choice,omitempty"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature *float64          `json:"temperature,omitempty"`
	TopP        *float64          `json:"top_p,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Thinking    *messagesThinking `json:"thinking,omitempty"`
}

type messagesThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type messagesMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type messagesTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

func reasoningBudget(effort string, maxTokens int) int {
	var budget int
	switch strings.ToLower(effort) {
	case "low", "minimal":
		budget = 2048
	case "high":
		budget = 16384
	case "xhigh", "max", "maximum":
		budget = 32768
	case "medium":
		budget = 8192
	default:
		return 0
	}
	if maxTokens > 0 && budget > maxTokens-1024 {
		budget = maxTokens - 1024
	}
	if budget < 1024 {
		budget = 1024
	}
	return budget
}

// ResponsesToMessagesRequest 把 Responses 请求转换为 Anthropic Messages 请求。
func ResponsesToMessagesRequest(body []byte, model string) ([]byte, error) {
	req, items, err := parseResponsesRequest(body)
	if err != nil {
		return nil, err
	}
	maxTokens := 8192
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		maxTokens = *req.MaxOutputTokens
	}
	out := messagesRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		System:      textOf(req.Instructions),
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" && req.Reasoning.Effort != "none" {
		if budget := reasoningBudget(req.Reasoning.Effort, maxTokens); budget > 0 {
			out.Thinking = &messagesThinking{Type: "enabled", BudgetTokens: budget}
		}
	}
	for _, t := range req.Tools {
		if t.Type != "" && t.Type != "function" {
			continue
		}
		out.Tools = append(out.Tools, messagesTool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters})
	}
	if len(req.ToolChoice) > 0 {
		var s string
		if json.Unmarshal(req.ToolChoice, &s) == nil {
			if s == "required" {
				out.ToolChoice = map[string]any{"type": "any"}
			} else if s != "" {
				out.ToolChoice = map[string]any{"type": "auto"}
			}
		} else {
			var obj struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(req.ToolChoice, &obj) == nil && obj.Name != "" {
				out.ToolChoice = map[string]any{"type": "tool", "name": obj.Name}
			}
		}
	}
	var pendingToolResults []any
	flushToolResults := func() {
		if len(pendingToolResults) > 0 {
			out.Messages = append(out.Messages, messagesMessage{Role: "user", Content: pendingToolResults})
			pendingToolResults = nil
		}
	}
	for _, it := range items {
		switch it.Type {
		case "", "message":
			flushToolResults()
			role := it.Role
			if role == "developer" || role == "system" {
				if out.System == "" {
					out.System = textOf(it.Content)
				} else {
					out.System += "\n" + textOf(it.Content)
				}
				continue
			}
			if role == "" {
				role = "user"
			}
			text := textOf(it.Content)
			out.Messages = append(out.Messages, messagesMessage{Role: role, Content: []any{map[string]any{"type": "text", "text": text}}})
		case "function_call":
			flushToolResults()
			var input any
			if it.Arguments != "" {
				_ = json.Unmarshal([]byte(it.Arguments), &input)
			}
			if input == nil {
				input = map[string]any{}
			}
			block := map[string]any{"type": "tool_use", "id": it.CallID, "name": it.Name, "input": input}
			if len(out.Messages) > 0 && out.Messages[len(out.Messages)-1].Role == "assistant" {
				out.Messages[len(out.Messages)-1].Content = append(out.Messages[len(out.Messages)-1].Content.([]any), block)
			} else {
				out.Messages = append(out.Messages, messagesMessage{Role: "assistant", Content: []any{block}})
			}
		case "function_call_output":
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type": "tool_result", "tool_use_id": it.CallID, "content": textOf(it.Output),
			})
		}
	}
	flushToolResults()
	if len(out.Messages) == 0 {
		out.Messages = []messagesMessage{{Role: "user", Content: []any{map[string]any{"type": "text", "text": ""}}}}
	}
	return marshal(out)
}

// ---- 请求转换：Chat → Messages ----

type simpleChatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Tools       []chatTool    `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

// ChatToMessagesRequest 把 Chat Completions 请求转换为 Messages 请求。
func ChatToMessagesRequest(body []byte, model string) ([]byte, error) {
	var req simpleChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 Chat 请求失败: %w", err)
	}
	maxTokens := 8192
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	out := messagesRequest{Model: model, MaxTokens: maxTokens, Temperature: req.Temperature, TopP: req.TopP, Stream: req.Stream}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, messagesTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: t.Function.Parameters})
	}
	var pendingToolResults []any
	flush := func() {
		if len(pendingToolResults) > 0 {
			out.Messages = append(out.Messages, messagesMessage{Role: "user", Content: pendingToolResults})
			pendingToolResults = nil
		}
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			if s, ok := m.Content.(string); ok && s != "" {
				if out.System == "" {
					out.System = s
				} else {
					out.System += "\n" + s
				}
			}
		case "tool":
			pendingToolResults = append(pendingToolResults, map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": contentString(m.Content)})
		case "assistant":
			flush()
			blocks := []any{}
			if s := contentString(m.Content); s != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": s})
			}
			for _, tc := range m.ToolCalls {
				var input any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input})
			}
			out.Messages = append(out.Messages, messagesMessage{Role: "assistant", Content: blocks})
		default:
			flush()
			out.Messages = append(out.Messages, messagesMessage{Role: "user", Content: []any{map[string]any{"type": "text", "text": contentString(m.Content)}}})
		}
	}
	flush()
	if len(out.Messages) == 0 {
		out.Messages = []messagesMessage{{Role: "user", Content: []any{map[string]any{"type": "text", "text": ""}}}}
	}
	return marshal(out)
}

func contentString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case nil:
		return ""
	default:
		b, _ := json.Marshal(c)
		return string(b)
	}
}

// ---- 请求转换：Chat → Responses ----

type simpleResponsesRequest struct {
	Model           string           `json:"model"`
	Instructions    string           `json:"instructions,omitempty"`
	Input           []map[string]any `json:"input"`
	Tools           []map[string]any `json:"tools,omitempty"`
	MaxOutputTokens *int             `json:"max_output_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	Stream          bool             `json:"stream,omitempty"`
}

// ChatToResponsesRequest 把 Chat Completions 请求转换为 Responses 请求。
func ChatToResponsesRequest(body []byte, model string) ([]byte, error) {
	var req simpleChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 Chat 请求失败: %w", err)
	}
	out := simpleResponsesRequest{Model: model, MaxOutputTokens: req.MaxTokens, Temperature: req.Temperature, TopP: req.TopP, Stream: req.Stream}
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			if s := contentString(m.Content); s != "" {
				if out.Instructions == "" {
					out.Instructions = s
				} else {
					out.Instructions += "\n" + s
				}
			}
		case "tool":
			out.Input = append(out.Input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": contentString(m.Content)})
		case "assistant":
			if s := contentString(m.Content); s != "" {
				out.Input = append(out.Input, textMessageItem("assistant", s))
			}
			for _, tc := range m.ToolCalls {
				out.Input = append(out.Input, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Function.Name, "arguments": tc.Function.Arguments})
			}
		default:
			out.Input = append(out.Input, textMessageItem("user", contentString(m.Content)))
		}
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, map[string]any{"type": "function", "name": t.Function.Name, "description": t.Function.Description, "parameters": t.Function.Parameters})
	}
	if len(out.Input) == 0 {
		out.Input = []map[string]any{textMessageItem("user", "")}
	}
	return marshal(out)
}

func textMessageItem(role, text string) map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": []map[string]any{{"type": "input_text", "text": text}},
	}
}

// PatchModel 在保持其余字段不变的情况下替换请求体中的 model。
func PatchModel(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("解析请求体失败: %w", err)
	}
	b, _ := json.Marshal(model)
	m["model"] = b
	return marshal(m)
}

// ---- 非流式响应转换 ----

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func responsesStatus(finish string) string {
	switch finish {
	case "length":
		return "incomplete"
	default:
		return "completed"
	}
}

func usageObject(in, out int) map[string]any {
	return map[string]any{"input_tokens": in, "output_tokens": out, "total_tokens": in + out}
}

// ChatToResponsesResponse 把 Chat 非流式响应转换为 Responses 响应。
func ChatToResponsesResponse(body []byte) ([]byte, error) {
	return ChatToResponsesResponseWith(body, ResponseConvertOptions{})
}

// ChatToResponsesResponseWith 支持把自定义（自由文本）工具调用还原为 custom_tool_call，
// 并把上游的思考内容回调给调用方（用于后续请求回填）。
func ChatToResponsesResponseWith(body []byte, opts ResponseConvertOptions) ([]byte, error) {
	var in chatResponse
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Chat 响应失败: %w", err)
	}
	out := map[string]any{
		"id":         ensurePrefix(in.ID, "resp_"),
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "completed",
		"model":      in.Model,
	}
	var output []any
	input, outputTokens := 0, 0
	if in.Usage != nil {
		input, outputTokens = in.Usage.PromptTokens, in.Usage.CompletionTokens
	}
	if len(in.Choices) > 0 {
		ch := in.Choices[0]
		out["status"] = responsesStatus(ch.FinishReason)
		if text := contentString(ch.Message.Content); text != "" {
			output = append(output, map[string]any{
				"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
				"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
			})
		}
		var callIDs []string
		for i, tc := range ch.Message.ToolCalls {
			callIDs = append(callIDs, tc.ID)
			if opts.CustomTools[tc.Function.Name] {
				output = append(output, map[string]any{
					"type": "custom_tool_call", "id": fmt.Sprintf("ctc_%d", i+1), "status": "completed",
					"call_id": tc.ID, "name": tc.Function.Name, "input": freeformInput(tc.Function.Arguments),
				})
				continue
			}
			output = append(output, map[string]any{
				"type": "function_call", "id": fmt.Sprintf("fc_%d", i+1), "status": "completed",
				"call_id": tc.ID, "name": tc.Function.Name, "arguments": tc.Function.Arguments,
			})
		}
		if opts.OnReasoning != nil {
			reasoningText := ch.Message.Reasoning
			if ch.Message.ReasoningContent != nil {
				reasoningText = *ch.Message.ReasoningContent
			}
			if reasoningText != "" {
				opts.OnReasoning(reasoningText, callIDs)
			}
		}
	}
	if output == nil {
		output = []any{}
	}
	out["output"] = output
	out["usage"] = usageObject(input, outputTokens)
	return marshal(out)
}

type messagesResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// MessagesToResponsesResponse 把 Messages 非流式响应转换为 Responses 响应。
func MessagesToResponsesResponse(body []byte) ([]byte, error) {
	var in messagesResponse
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Messages 响应失败: %w", err)
	}
	out := map[string]any{
		"id":         ensurePrefix(in.ID, "resp_"),
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "completed",
		"model":      in.Model,
	}
	var output []any
	inputTok, outputTok := 0, 0
	if in.Usage != nil {
		inputTok, outputTok = in.Usage.InputTokens, in.Usage.OutputTokens
	}
	if in.StopReason == "max_tokens" {
		out["status"] = "incomplete"
	}
	text := ""
	for _, c := range in.Content {
		switch c.Type {
		case "text":
			text += c.Text
		case "tool_use":
			output = append(output, map[string]any{
				"type": "function_call", "id": "fc_" + c.ID, "status": "completed",
				"call_id": c.ID, "name": c.Name, "arguments": rawToString(c.Input),
			})
		}
	}
	if text != "" {
		output = append([]any{map[string]any{
			"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
		}}, output...)
	}
	if output == nil {
		output = []any{}
	}
	out["output"] = output
	out["usage"] = usageObject(inputTok, outputTok)
	return marshal(out)
}

// ResponsesToChatResponse 把 Responses 非流式响应转换为 Chat 响应。
func ResponsesToChatResponse(body []byte) ([]byte, error) {
	var in struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Output []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Responses 响应失败: %w", err)
	}
	msg := chatMessage{Role: "assistant"}
	for _, item := range in.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" || c.Type == "text" {
					msg.Content = contentString(msg.Content) + c.Text
				}
			}
		case "function_call":
			var tc chatToolCall
			tc.ID = item.CallID
			tc.Type = "function"
			tc.Function.Name = item.Name
			tc.Function.Arguments = item.Arguments
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	finish := "stop"
	if len(msg.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	if in.Status == "incomplete" {
		finish = "length"
	}
	usage := map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	if in.Usage != nil {
		usage = map[string]any{"prompt_tokens": in.Usage.InputTokens, "completion_tokens": in.Usage.OutputTokens, "total_tokens": in.Usage.TotalTokens}
	}
	out := map[string]any{
		"id":      ensurePrefix(in.ID, "chatcmpl_"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   in.Model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   usage,
	}
	return marshal(out)
}

// MessagesToChatResponse 把 Messages 非流式响应转换为 Chat 响应。
func MessagesToChatResponse(body []byte) ([]byte, error) {
	var in messagesResponse
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Messages 响应失败: %w", err)
	}
	msg := chatMessage{Role: "assistant"}
	for _, c := range in.Content {
		switch c.Type {
		case "text":
			msg.Content = contentString(msg.Content) + c.Text
		case "tool_use":
			var tc chatToolCall
			tc.ID = c.ID
			tc.Type = "function"
			tc.Function.Name = c.Name
			tc.Function.Arguments = rawToString(c.Input)
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	finish := "stop"
	switch in.StopReason {
	case "tool_use":
		finish = "tool_calls"
	case "max_tokens":
		finish = "length"
	}
	usage := map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	if in.Usage != nil {
		usage = map[string]any{"prompt_tokens": in.Usage.InputTokens, "completion_tokens": in.Usage.OutputTokens, "total_tokens": in.Usage.InputTokens + in.Usage.OutputTokens}
	}
	out := map[string]any{
		"id":      ensurePrefix(in.ID, "chatcmpl_"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   in.Model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   usage,
	}
	return marshal(out)
}

// ConvertErrorBody 把上游错误体转换为入口协议的错误格式。
func ConvertErrorBody(entry Protocol, status int, upstream []byte) []byte {
	msg := strings.TrimSpace(string(upstream))
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(upstream, &parsed); err == nil {
		switch {
		case parsed.Error != nil && parsed.Error.Message != "":
			msg = parsed.Error.Message
		case parsed.Message != "":
			msg = parsed.Message
		}
	}
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	if msg == "" {
		msg = fmt.Sprintf("上游返回 HTTP %d", status)
	}
	typ := "api_error"
	if status >= 400 && status < 500 {
		typ = "invalid_request_error"
	}
	out, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": typ, "code": nil}})
	return out
}

func ensurePrefix(id, prefix string) string {
	if id == "" {
		return prefix + fmt.Sprint(time.Now().UnixNano())
	}
	if strings.HasPrefix(id, prefix) {
		return id
	}
	return prefix + id
}

func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
