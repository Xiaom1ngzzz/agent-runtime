package prompt

import (
	"encoding/json"

	"agent-runtime-go/domain"
)

// AnthropicCompiler —— 把 Context 编成 Anthropic Messages API 的请求体。
//
// 关键差异(见 ch06 §6.6.1):
//   - system 独立字段,不进 messages 数组
//   - message.content 是数组,tool_use / tool_result 是数组项
//   - Tool schema key 是 input_schema
//   - 不允许连续 user(违反则 §6.4 报错)
type AnthropicCompiler struct {
	Model string
}

// CompileToProvider 生成 Anthropic 请求体。这是 Compile 的"扩展签名"——
// 生产上业务通常调 Compile(仅返回 Messages),Provider 特有信息在客户端组装;
// 这里为了让读者能直接对比两家 Provider 的输出,暴露一个显式 API。
func (c AnthropicCompiler) CompileToProvider(ctx domain.Context) (AnthropicRequest, error) {
	if err := checkMessages(ctx.Messages); err != nil {
		return AnthropicRequest{}, err
	}
	if err := checkNoConsecutiveUser(ctx.Messages); err != nil {
		return AnthropicRequest{}, err
	}
	req := AnthropicRequest{Model: c.Model}

	// 1. system —— 收集所有 role=system 的 content,拼成 system 字段。
	//    这就是 Anthropic 的规范:system 独立,不进 messages。
	var sysParts []string
	for _, m := range ctx.Messages {
		if m.Role == "system" {
			sysParts = append(sysParts, m.Content)
		}
	}
	req.System = joinStrings(sysParts, "\n\n")

	// 2. messages —— user / assistant / tool 转成 Anthropic 的 content-array 形式。
	for _, m := range ctx.Messages {
		switch m.Role {
		case "system":
			// 已进 System 字段
		case "user":
			req.Messages = append(req.Messages, AnthropicMessage{
				Role:    "user",
				Content: []AnthropicContent{{Type: "text", Text: m.Content}},
			})
		case "assistant":
			contents := []AnthropicContent{}
			if m.Content != "" {
				contents = append(contents, AnthropicContent{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				contents = append(contents, AnthropicContent{
					Type:      "tool_use",
					ToolUseID: tc.ID,
					ToolName:  tc.Name,
					ToolInput: tc.Arguments,
				})
			}
			req.Messages = append(req.Messages, AnthropicMessage{
				Role: "assistant", Content: contents,
			})
		case "tool":
			// Anthropic 里,tool_result 作为 user message 的 content 数组项
			req.Messages = append(req.Messages, AnthropicMessage{
				Role: "user",
				Content: []AnthropicContent{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
		}
	}

	// 3. tools —— key 是 input_schema
	for _, t := range ctx.Tools {
		var schema map[string]any
		if t.Schema != nil {
			schema = t.Schema
		} else {
			schema = map[string]any{"type": "object"}
		}
		req.Tools = append(req.Tools, AnthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	return req, nil
}

// Compile 是标准 PromptCompiler 接口 —— 只输出中立 Messages。
// 上层要拿 Anthropic 专用字段时调 CompileToProvider。
func (c AnthropicCompiler) Compile(ctx domain.Context) (Messages, error) {
	if err := checkMessages(ctx.Messages); err != nil {
		return nil, err
	}
	if err := checkNoConsecutiveUser(ctx.Messages); err != nil {
		return nil, err
	}
	return Messages(ctx.Messages), nil
}

// checkNoConsecutiveUser 是 Anthropic 特有的 Type-check。
// OpenAICompiler 不做这个检查(见 §6.6.1 表格)。
func checkNoConsecutiveUser(msgs []domain.Message) error {
	prev := ""
	for i, m := range msgs {
		if m.Role == "user" && prev == "user" {
			return &PromptCheckError{
				Index: i, Field: "role",
				Msg: "anthropic: consecutive user messages not allowed",
			}
		}
		prev = m.Role
	}
	return nil
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}

// 显式引用 json,避免 lint 抱怨(将来 Marshaling 会用)
var _ = json.Marshal
