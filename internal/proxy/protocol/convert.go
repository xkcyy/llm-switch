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

// imagePart 兼容三种协议里的图片内容块：
// Responses 的 input_image（image_url 为字符串）、Chat 的 image_url（字符串或对象）、
// Messages 的 image（不在请求解码路径上，仅解码 Responses/Chat 用不到）。
type imagePart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
	Detail   string          `json:"detail"`
}

// imageURLObj 是 Chat 的 image_url 对象写法：{"url": "...", "detail": "auto"}。
type imageURLObj struct {
	URL    string `json:"url"`
	Detail string `json:"detail"`
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

// splitResponsesContent 解析 Responses 的 content：返回文本与图片。
// content 允许是纯字符串或内容块数组；无法识别的块按文本字段兜底。
func splitResponsesContent(raw json.RawMessage) (string, []irImage) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var parts []imagePart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return textOf(raw), nil
	}
	var b strings.Builder
	var images []irImage
	for _, p := range parts {
		switch p.Type {
		case "input_image", "image_url", "image":
			if img, ok := decodeImagePart(p); ok {
				images = append(images, img)
			}
		default:
			if img, ok := decodeImagePart(p); ok {
				images = append(images, img)
				continue
			}
			b.WriteString(p.Text)
		}
	}
	if b.Len() == 0 && len(images) == 0 {
		// 兼容老结构：内容块数组里只有 text 字段
		return textOf(raw), nil
	}
	return b.String(), images
}

// splitChatContent 解析 Chat 的 content：纯字符串、内容块数组或单个内容块对象。
// 非字符串且非数组时保持 contentString 的旧行为（JSON 文本）。
func splitChatContent(v any) (string, []irImage) {
	switch c := v.(type) {
	case nil:
		return "", nil
	case string:
		return c, nil
	case []any:
		var b strings.Builder
		var images []irImage
		for _, part := range c {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			raw, _ := json.Marshal(m)
			if img, ok := decodeImagePartRaw(raw); ok {
				images = append(images, img)
				continue
			}
			if t, _ := m["text"].(string); t != "" {
				b.WriteString(t)
			}
		}
		if b.Len() == 0 && len(images) == 0 {
			return contentString(v), nil
		}
		return b.String(), images
	case map[string]any:
		raw, _ := json.Marshal(c)
		if img, ok := decodeImagePartRaw(raw); ok {
			return "", []irImage{img}
		}
		return contentString(v), nil
	default:
		return contentString(v), nil
	}
}

// decodeImagePart 从已解码的内容块取图片；不是图片块时 ok=false。
func decodeImagePart(p imagePart) (irImage, bool) {
	if !isImageType(p.Type) {
		return irImage{}, false
	}
	if len(p.ImageURL) == 0 {
		return irImage{}, false
	}
	var s string
	if err := json.Unmarshal(p.ImageURL, &s); err == nil {
		if s == "" {
			return irImage{}, false
		}
		return irImage{URL: s, Detail: p.Detail}, true
	}
	var obj imageURLObj
	if err := json.Unmarshal(p.ImageURL, &obj); err != nil || obj.URL == "" {
		return irImage{}, false
	}
	detail := obj.Detail
	if detail == "" {
		detail = p.Detail
	}
	return irImage{URL: obj.URL, Detail: detail}, true
}

func decodeImagePartRaw(raw json.RawMessage) (irImage, bool) {
	var p imagePart
	if err := json.Unmarshal(raw, &p); err != nil {
		return irImage{}, false
	}
	return decodeImagePart(p)
}

func isImageType(t string) bool {
	switch t {
	case "input_image", "image_url", "image":
		return true
	}
	return false
}

// rawOf 把单个内容块包成完整 JSON，供 textOf 复用。
// responsesContentParts 生成 Responses 的 content 内容块数组。
func responsesContentParts(text string, images []irImage) []map[string]any {
	parts := make([]map[string]any, 0, len(images)+1)
	if text != "" || len(images) == 0 {
		parts = append(parts, map[string]any{"type": "input_text", "text": text})
	}
	for _, img := range images {
		part := map[string]any{"type": "input_image", "image_url": img.URL}
		if img.Detail != "" {
			part["detail"] = img.Detail
		}
		parts = append(parts, part)
	}
	return parts
}

// chatContent 生成 Chat 的 content：无图片时保持纯字符串（与既有形态一致）。
func chatContent(text string, images []irImage) any {
	if len(images) == 0 {
		return text
	}
	parts := make([]map[string]any, 0, len(images)+1)
	if text != "" {
		parts = append(parts, map[string]any{"type": "text", "text": text})
	}
	for _, img := range images {
		url := map[string]any{"url": img.URL}
		if img.Detail != "" {
			url["detail"] = img.Detail
		}
		parts = append(parts, map[string]any{"type": "image_url", "image_url": url})
	}
	return parts
}

// messagesContentBlocks 生成 Messages 的 content 内容块数组：
// data URI 转成 base64 图片块，其余地址按 URL 图片块。
func messagesContentBlocks(text string, images []irImage) []any {
	blocks := make([]any, 0, len(images)+1)
	if text != "" || len(images) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, img := range images {
		blocks = append(blocks, messagesImageBlock(img))
	}
	return blocks
}

// messagesImageBlock 生成单个 Messages 图片块：data URI 转 base64，其余按 URL。
func messagesImageBlock(img irImage) map[string]any {
	if mediaType, data, ok := parseDataURI(img.URL); ok {
		return map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "base64", "media_type": mediaType, "data": data},
		}
	}
	return map[string]any{
		"type":   "image",
		"source": map[string]any{"type": "url", "url": img.URL},
	}
}

// parseDataURI 拆分 data:image/png;base64,xxxx 形式的地址。
func parseDataURI(url string) (string, string, bool) {
	if !strings.HasPrefix(url, "data:") {
		return "", "", false
	}
	header, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
	if !ok || !strings.Contains(header, ";base64") {
		return "", "", false
	}
	mediaType := strings.TrimSuffix(header, ";base64")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return mediaType, data, true
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
	Summary   json.RawMessage `json:"summary"`
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
			text, images := splitResponsesContent(it.Content)
			out.Messages = append(out.Messages, messagesMessage{Role: role, Content: messagesContentBlocks(text, images)})
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
			text, images := splitChatContent(m.Content)
			if s := text; s != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": s})
			}
			for _, img := range images {
				blocks = append(blocks, messagesImageBlock(img))
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
			text, images := splitChatContent(m.Content)
			out.Messages = append(out.Messages, messagesMessage{Role: "user", Content: messagesContentBlocks(text, images)})
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

func textMessageItem(role, text string) map[string]any {
	return messageItem(role, text, nil)
}

// messageItem 生成 Responses 的 message 条目，文本与图片按顺序进 content 块数组。
func messageItem(role, text string, images []irImage) map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": responsesContentParts(text, images),
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

func usageObject(in, out int) map[string]any {
	return map[string]any{"input_tokens": in, "output_tokens": out, "total_tokens": in + out}
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
