package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TraceDocument 是一条入站请求解析出的轨迹：合并后的系统提示词、工具目录与历史行。
// 只服务管理界面的「轨迹」视图，与协议转换无关；直通路径同样可解析。
type TraceDocument struct {
	System string      `json:"system"`
	Tools  []TraceTool `json:"tools"`
	Rows   []TraceRow  `json:"rows"`
}

// TraceTool 是请求声明的工具目录项。
type TraceTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// 轨迹行类型。system 包含 instructions 与 developer/system 消息；reasoning 是思考内容。
const (
	traceSystem     = "system"
	traceUser       = "user"
	traceAssistant  = "assistant"
	traceReasoning  = "reasoning"
	traceToolCall   = "tool-call"
	traceToolResult = "tool-result"
	traceOther      = "other"
)

// TraceRow 是轨迹里的一行：一条消息、一次工具调用、一条工具结果或一段思考内容。
type TraceRow struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	Label     string `json:"label,omitempty"` // 原始条目类型，仅 other 使用
	Role      string `json:"role,omitempty"`
	Text      string `json:"text,omitempty"`
	Name      string `json:"name,omitempty"` // 工具名
	CallID    string `json:"call_id,omitempty"`
	Arguments string `json:"arguments,omitempty"` // 工具调用参数
	Output    string `json:"output,omitempty"`    // 工具结果
	Images    int    `json:"images,omitempty"`
}

// TraceRequest 解析一条入站请求体，产出轨迹文档。三种入口协议都支持。
func TraceRequest(entry Protocol, body []byte) (*TraceDocument, error) {
	switch entry {
	case Chat, Responses:
		req, err := decodeRequest(entry, body)
		if err != nil {
			return nil, err
		}
		return traceFromIR(req), nil
	case Messages:
		return traceFromMessages(body)
	}
	return nil, fmt.Errorf("未知协议 %q", entry)
}

func traceFromIR(req *irRequest) *TraceDocument {
	systemParts := []string{}
	if req.Instructions != "" {
		systemParts = append(systemParts, req.Instructions)
	}
	items := append([]irItem(nil), req.Items...)
	for i := range items {
		it := &items[i]
		if it.Role == "user" {
			var reminders []string
			it.Text, reminders = extractSystemReminders(it.Text)
			systemParts = append(systemParts, reminders...)
		}
		if it.Kind == irMessage && (it.Role == "developer" || it.Role == "system") && it.Text != "" {
			systemParts = append(systemParts, it.Text)
		}
	}
	doc := &TraceDocument{System: strings.Join(systemParts, "\n\n"), Rows: []TraceRow{}}
	for _, t := range req.Tools {
		doc.Tools = append(doc.Tools, TraceTool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	add := func(row TraceRow) {
		row.Index = len(doc.Rows) + 1
		doc.Rows = append(doc.Rows, row)
	}
	if doc.System != "" {
		add(TraceRow{Kind: traceSystem, Text: doc.System})
	}
	for _, it := range items {
		switch it.Kind {
		case irMessage:
			if it.Role == "developer" || it.Role == "system" {
				continue
			}
			kind := traceUser
			switch it.Role {
			case "assistant":
				kind = traceAssistant
			}
			add(TraceRow{Kind: kind, Role: it.Role, Text: it.Text, Images: len(it.Images)})
		case irFunctionCall:
			add(TraceRow{Kind: traceToolCall, Name: it.Name, CallID: it.CallID, Arguments: it.Arguments})
		case irCustomCall:
			add(TraceRow{Kind: traceToolCall, Name: it.Name, CallID: it.CallID, Arguments: it.Input})
		case irCallOutput, irCustomOutput:
			add(TraceRow{Kind: traceToolResult, CallID: it.CallID, Output: it.Output})
		case "reasoning":
			add(TraceRow{Kind: traceReasoning, Text: it.Text})
		default:
			add(TraceRow{Kind: traceOther, Label: it.Kind})
		}
	}
	return doc
}

// extractSystemReminders moves explicit system-reminder blocks out of user text
// for display in the combined system prompt.
func extractSystemReminders(text string) (string, []string) {
	const (
		openTag  = "<system-reminder>"
		closeTag = "</system-reminder>"
	)
	var remaining strings.Builder
	var reminders []string
	cursor := 0
	for cursor < len(text) {
		start := strings.Index(text[cursor:], openTag)
		if start < 0 {
			break
		}
		start += cursor
		contentStart := start + len(openTag)
		end := strings.Index(text[contentStart:], closeTag)
		if end < 0 {
			break
		}
		end += contentStart + len(closeTag)
		remaining.WriteString(text[cursor:start])
		reminders = append(reminders, text[start:end])
		cursor = end
	}
	if len(reminders) == 0 {
		return text, nil
	}
	remaining.WriteString(text[cursor:])
	return strings.TrimSpace(remaining.String()), reminders
}

// ---- Messages（Anthropic）入口 ----

type messagesTraceRequest struct {
	Model    string          `json:"model"`
	System   json.RawMessage `json:"system,omitempty"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"input_schema,omitempty"`
	} `json:"tools"`
}

type messagesTraceBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

func traceFromMessages(body []byte) (*TraceDocument, error) {
	var in messagesTraceRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Messages 请求失败: %w", err)
	}
	doc := &TraceDocument{System: textOf(in.System), Rows: []TraceRow{}}
	for _, t := range in.Tools {
		doc.Tools = append(doc.Tools, TraceTool{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
	}
	add := func(row TraceRow) {
		row.Index = len(doc.Rows) + 1
		doc.Rows = append(doc.Rows, row)
	}
	if doc.System != "" {
		add(TraceRow{Kind: traceSystem, Text: doc.System})
	}
	for _, m := range in.Messages {
		kind := traceUser
		if m.Role == "assistant" {
			kind = traceAssistant
		}
		// content 可能是纯字符串
		var text string
		if json.Unmarshal(m.Content, &text) == nil {
			add(TraceRow{Kind: kind, Role: m.Role, Text: text})
			continue
		}
		var blocks []messagesTraceBlock
		if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				add(TraceRow{Kind: kind, Role: m.Role, Text: b.Text})
			case "thinking", "redacted_thinking":
				add(TraceRow{Kind: traceReasoning, Text: b.Thinking})
			case "tool_use":
				add(TraceRow{Kind: traceToolCall, Name: b.Name, CallID: b.ID, Arguments: string(b.Input)})
			case "tool_result":
				add(TraceRow{Kind: traceToolResult, CallID: b.ToolUseID, Output: textOf(b.Content)})
			case "image":
				add(TraceRow{Kind: kind, Role: m.Role, Images: 1})
			default:
				add(TraceRow{Kind: traceOther, Label: b.Type})
			}
		}
	}
	return doc, nil
}
