package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"llm-switch/internal/proxy/protocol"
)

func TestPreviewTraceDocumentLimitsPayload(t *testing.T) {
	rows := make([]protocol.TraceRow, 3000)
	for i := range rows {
		rows[i] = protocol.TraceRow{
			Index:  i + 1,
			Kind:   "user",
			Text:   strings.Repeat("请求内容", 12000),
			Output: strings.Repeat("工具输出", 12000),
		}
	}
	doc := &protocol.TraceDocument{
		System: strings.Repeat("系统提示", 20000),
		Tools: []protocol.TraceTool{{
			Name:        "read",
			Description: strings.Repeat("工具说明", 20000),
			Parameters:  []byte(`{"type":"object"}`),
		}},
		Rows: rows,
	}
	out := previewTraceDocument(doc)
	if !out.Truncated {
		t.Fatal("超大轨迹应标记截断")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 3<<19 {
		t.Fatalf("轨迹详情仍然过大: %d bytes", len(raw))
	}
	if len(out.Rows) != tracePreviewMaxRows {
		t.Fatalf("轨迹行数未截断: %d", len(out.Rows))
	}
	if !utf8.Valid(raw) || !utf8.ValidString(out.System) {
		t.Fatal("截断后出现非法 UTF-8")
	}
}
