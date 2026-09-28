package protocol

import "encoding/json"

// IR 是转换路径（入口协议 ≠ 上游协议）的中间形态：请求、响应与流式事件各一份。
// 词汇取自 Responses，字段集严格等于 Responses 公开字段；同协议直通不经过 IR。
// 见 docs/adr/0001-responses-as-ir.md 与设计文档 §9.1。

// 条目类型。除下列取值外，条目 Kind 原样保留 Responses 的 type
// （reasoning、web_search_call 等），由各协议编码器决定忽略还是转换。
const (
	irMessage      = "message"
	irFunctionCall = "function_call"
	irCallOutput   = "function_call_output"
	irCustomCall   = "custom_tool_call"
	irCustomOutput = "custom_tool_call_output"
)

type irItem struct {
	Kind      string
	Role      string    // message
	Text      string    // message 文本
	Images    []irImage // message 图片（按出现顺序，紧随文本之后）
	CallID    string
	Name      string
	Arguments string // function_call 的参数（JSON 文本）
	Input     string // custom_tool_call 的自由文本
	Output    string // *_output 的内容
}

// irImage 是消息里的一张图片。URL 既可为 data URI（data:image/png;base64,...），
// 也可为 http(s) 地址；三种上游协议都能表达这两者。
type irImage struct {
	URL    string
	Detail string // Responses / Chat 的 detail（auto|low|high），可空
}

type irTool struct {
	Kind        string // function | custom
	Name        string
	Description string
	Parameters  json.RawMessage
}

type irToolChoice struct {
	Mode string // auto | required | none | tool | 其它 Responses 取值
	Name string
}

type irRequest struct {
	Model             string
	Instructions      string
	Items             []irItem
	Tools             []irTool
	ToolChoice        *irToolChoice
	ParallelToolCalls *bool
	MaxOutputTokens   *int
	Temperature       *float64
	TopP              *float64
	Stream            bool
	ReasoningEffort   string
}

type irResponse struct {
	ID           string
	Model        string
	Status       string // completed | incomplete
	Text         string
	Calls        []irItem // function_call（custom 由编码器按请求侧工具名判定）
	Reasoning    string
	InputTokens  int
	OutputTokens int
}

// 流式事件：粗粒度语义事件，各协议编码器自行维护输出侧状态机
// （Responses 的 output_item 生命周期、Chat 的单 delta 累积）。
const (
	irStart     = "start"      // 首个事件，携带响应 ID 与模型
	irText      = "text"       // 文本增量
	irReasoning = "reasoning"  // 思考内容增量
	irCallStart = "call_start" // 工具调用出现或元数据补全（可能重复出现同一 Index）
	irCallArgs  = "call_args"  // 工具调用参数增量
	irFinish    = "finish"     // 结束原因
	irUsage     = "usage"      // 用量
)

type irEvent struct {
	Kind         string
	ID           string
	Model        string
	Text         string
	Index        int
	CallID       string
	Name         string
	Finish       string
	InputTokens  int
	OutputTokens int
}

// customToolNames 返回请求中声明为 custom（自由文本）的工具名集合。
func (r *irRequest) customToolNames() map[string]bool {
	names := map[string]bool{}
	for _, t := range r.Tools {
		if t.Kind == "custom" && t.Name != "" {
			names[t.Name] = true
		}
	}
	return names
}

// droppedItem 判断该条目是否在转换中被静默忽略（不参与工具调用成组）。
func droppedItem(kind string) bool {
	switch kind {
	case "reasoning", "web_search_call", "local_shell_call", "computer_call",
		"ghost_snapshot", "mcp_call", "mcp_list_tools", "mcp_approval_request",
		"mcp_approval_response", "code_interpreter_call", "image_generation_call":
		return true
	}
	return false
}
