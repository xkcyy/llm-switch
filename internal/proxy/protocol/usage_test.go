package protocol

import "testing"

func TestParseUsageFieldNames(t *testing.T) {
	cases := []struct {
		name string
		body string
		want CallUsage
		ok   bool
	}{
		{"chat 形状", `{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			CallUsage{Input: 10, Output: 5, Total: 15}, true},
		{"responses 形状", `{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`,
			CallUsage{Input: 10, Output: 5, Total: 15}, true},
		{"缺 total 时补齐", `{"usage":{"input_tokens":10,"output_tokens":5}}`,
			CallUsage{Input: 10, Output: 5, Total: 15}, true},
		{"details 与顶层不重复累加", `{"usage":{"input_tokens":10,"output_tokens":5,"cached_tokens":3,"input_tokens_details":{"cached_tokens":3}}}`,
			CallUsage{Input: 10, Output: 5, Total: 15, Cached: 3}, true},
		{"推理 token", `{"usage":{"input_tokens":1,"output_tokens":9,"completion_tokens_details":{"reasoning_tokens":7}}}`,
			CallUsage{Input: 1, Output: 9, Total: 10, Reasoning: 7}, true},
		{"流式事件内嵌 response.usage", `{"type":"response.completed","response":{"usage":{"input_tokens":4,"output_tokens":2}}}`,
			CallUsage{Input: 4, Output: 2, Total: 6}, true},
		{"Anthropic message_start", `{"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":1}}}`,
			CallUsage{Input: 11, Output: 1, Total: 12}, true},
		{"没有 usage", `{"id":"c1","choices":[]}`, CallUsage{}, false},
		{"非 JSON", `not json`, CallUsage{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseUsage([]byte(tc.body))
			if ok != tc.ok {
				t.Fatalf("ok = %v，期望 %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("usage = %+v，期望 %+v", got, tc.want)
			}
		})
	}
}
