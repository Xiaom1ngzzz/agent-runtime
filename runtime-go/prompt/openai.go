package prompt

import "agent-runtime-go/domain"

// OpenAICompiler —— 把 Context 编成 OpenAI chat.completions 请求体。
//
// 关键差异(见 ch06 §6.6.1):
//   - system 作为 messages[0]
//   - assistant.tool_calls 是独立字段,不是 content 数组项
//   - tool response 是独立 role=tool 的消息
//   - Tool schema key 是 parameters
//   - 允许连续 user(会被合并)
type OpenAICompiler struct {
	Model string
}

func (c OpenAICompiler) CompileToProvider(ctx domain.Context) (OpenAIRequest, error) {
	if err := checkMessages(ctx.Messages); err != nil {
		return OpenAIRequest{}, err
	}
	req := OpenAIRequest{Model: c.Model}

	for _, m := range ctx.Messages {
		switch m.Role {
		case "system":
			req.Messages = append(req.Messages, OpenAIMessage{
				Role: "system", Content: m.Content,
			})
		case "user":
			req.Messages = append(req.Messages, OpenAIMessage{
				Role: "user", Content: m.Content,
			})
		case "assistant":
			om := OpenAIMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, OpenAIToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: OpenAIFunction{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
			req.Messages = append(req.Messages, om)
		case "tool":
			req.Messages = append(req.Messages, OpenAIMessage{
				Role:       "tool",
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			})
		}
	}

	for _, t := range ctx.Tools {
		var params map[string]any
		if t.Schema != nil {
			params = t.Schema
		} else {
			params = map[string]any{"type": "object"}
		}
		req.Tools = append(req.Tools, OpenAITool{
			Type: "function",
			Function: OpenAIFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	return req, nil
}

func (c OpenAICompiler) Compile(ctx domain.Context) (Messages, error) {
	if err := checkMessages(ctx.Messages); err != nil {
		return nil, err
	}
	return Messages(ctx.Messages), nil
}
