// runtime-go/prompt/provider_request.go —— Provider-specific 请求体的中立表达。
//
// 不发真请求,只生成"如果发的话"的 payload,便于对比不同 Provider 的差异。
// 见 ch06 §6.6。
package prompt

import "agent-runtime-go/domain"

// AnthropicRequest 近似 Anthropic Messages API 的请求体。
type AnthropicRequest struct {
	Model    string
	System   string // 独立字段,不进 messages 数组
	Messages []AnthropicMessage
	Tools    []AnthropicTool
}

type AnthropicMessage struct {
	Role    string             // "user" | "assistant" (无 "system")
	Content []AnthropicContent // Anthropic 用 content 数组,每项带 type
}

type AnthropicContent struct {
	Type      string // "text" | "tool_use" | "tool_result"
	Text      string // for type=text
	ToolUseID string // for type=tool_use / tool_result
	ToolName  string // for type=tool_use
	ToolInput string // for type=tool_use, JSON 字符串
	Content   string // for type=tool_result, 结果内容
}

type AnthropicTool struct {
	Name        string
	Description string
	InputSchema map[string]any // Anthropic 用 input_schema
}

// OpenAIRequest 近似 OpenAI chat.completions 请求体。
type OpenAIRequest struct {
	Model    string
	Messages []OpenAIMessage
	Tools    []OpenAITool
}

type OpenAIMessage struct {
	Role       string // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCalls  []OpenAIToolCall // assistant 消息里的工具调用
	ToolCallID string           // role=tool 时对齐上一条 tool_calls[i].id
}

type OpenAIToolCall struct {
	ID       string
	Type     string // "function"
	Function OpenAIFunction
}

type OpenAIFunction struct {
	Name      string
	Arguments string // JSON 字符串
}

type OpenAITool struct {
	Type     string // "function"
	Function OpenAIFunctionDef
}

type OpenAIFunctionDef struct {
	Name        string
	Description string
	Parameters  map[string]any // OpenAI 用 parameters
}

// checkMessages 是 ch06 §6.4 的基线校验,两个 Provider 共用。
// 违反 → PromptError,业务侧感知,不发到 Provider。
func checkMessages(msgs []domain.Message) error {
	// 1. role 合法
	for i, m := range msgs {
		switch m.Role {
		case "system", "user", "assistant", "tool":
		default:
			return &PromptCheckError{
				Index: i,
				Field: "role",
				Msg:   "unknown role: " + m.Role,
			}
		}
	}
	// 2. role=tool 必须带 ToolCallID + 匹配上一条 assistant 的 tool_calls
	for i, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if m.ToolCallID == "" {
			return &PromptCheckError{Index: i, Field: "tool_call_id", Msg: "role=tool without tool_call_id"}
		}
		// 反向找上一条 assistant with tool_calls
		matched := false
		for j := i - 1; j >= 0; j-- {
			if msgs[j].Role == "assistant" {
				for _, tc := range msgs[j].ToolCalls {
					if tc.ID == m.ToolCallID {
						matched = true
					}
				}
				break
			}
		}
		if !matched {
			return &PromptCheckError{
				Index: i, Field: "tool_call_id",
				Msg: "tool_call_id " + m.ToolCallID + " has no matching assistant.tool_calls[]",
			}
		}
	}
	return nil
}

// PromptCheckError 是 §6.4 反例正解:错误带具体字段。
type PromptCheckError struct {
	Index int
	Field string
	Msg   string
}

func (e *PromptCheckError) Error() string {
	return "prompt check failed at message[" + itoa(e.Index) + "]." + e.Field + ": " + e.Msg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
