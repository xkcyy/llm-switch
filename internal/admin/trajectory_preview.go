package admin

import (
	"unicode/utf8"

	"llm-switch/internal/proxy/protocol"
)

const (
	tracePreviewTotalBytes = 1 << 20
	tracePreviewFieldBytes = 64 << 10
	tracePreviewMaxRows    = 2000
	tracePreviewMaxTools   = 500
	tracePreviewCutSuffix  = "\n…（已截断，完整内容见原始报文）"
)

// previewTraceDocument 限制轨迹详情响应体大小。详情用于快速浏览，
// 大字段和超量行不再随每个请求重复传给浏览器；完整内容走原始报文接口。
func previewTraceDocument(doc *protocol.TraceDocument) *protocol.TraceDocument {
	if doc == nil {
		return nil
	}
	budget := tracePreviewTotalBytes
	out := &protocol.TraceDocument{
		Tools: make([]protocol.TraceTool, 0, min(len(doc.Tools), tracePreviewMaxTools)),
		Rows:  make([]protocol.TraceRow, 0, min(len(doc.Rows), tracePreviewMaxRows)),
	}
	var clipped bool
	out.System, clipped = clipTraceText(doc.System, &budget)
	out.Truncated = clipped

	for i, tool := range doc.Tools {
		if i >= tracePreviewMaxTools {
			out.Truncated = true
			break
		}
		item := protocol.TraceTool{Name: tool.Name}
		var toolClipped bool
		item.Description, toolClipped = clipTraceText(tool.Description, &budget)
		if len(tool.Parameters) > 0 {
			params, paramsClipped := clipTraceText(string(tool.Parameters), &budget)
			item.Parameters = []byte(params)
			toolClipped = toolClipped || paramsClipped
		}
		item.Truncated = toolClipped
		out.Truncated = out.Truncated || toolClipped
		out.Tools = append(out.Tools, item)
	}

	for i, row := range doc.Rows {
		if i >= tracePreviewMaxRows {
			out.Truncated = true
			break
		}
		item := protocol.TraceRow{
			Index: row.Index, Kind: row.Kind, Label: row.Label, Role: row.Role,
			Name: row.Name, CallID: row.CallID, Images: row.Images,
		}
		var rowClipped bool
		item.Text, rowClipped = clipTraceText(row.Text, &budget)
		rowClipped = rowClipped || keepClip(&item.Arguments, row.Arguments, &budget)
		rowClipped = rowClipped || keepClip(&item.Output, row.Output, &budget)
		item.Truncated = rowClipped
		out.Truncated = out.Truncated || rowClipped
		out.Rows = append(out.Rows, item)
	}
	return out
}

func keepClip(dst *string, src string, budget *int) bool {
	var clipped bool
	*dst, clipped = clipTraceText(src, budget)
	return clipped
}

func clipTraceText(text string, budget *int) (string, bool) {
	if text == "" {
		return "", false
	}
	if *budget <= 0 {
		return "", true
	}
	limit := min(tracePreviewFieldBytes, *budget)
	if len(text) <= limit {
		*budget -= len(text)
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	out := text[:cut] + tracePreviewCutSuffix
	*budget -= len(out)
	return out, true
}
