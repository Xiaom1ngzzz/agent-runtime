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
//   - 并行 tool_result 聚合到同一 user message(Provider 也会合并连续 user)
type AnthropicCompiler struct {
	Model string
}

func (c AnthropicCompiler) CompileToProvider(ctx domain.Context) (AnthropicRequest, error) {
	if err := checkMessages(ctx.Messages); err != nil {
		return AnthropicRequest{}, err
	}
	req := AnthropicRequest{Model: c.Model}

	var sysParts []string
	for _, m := range ctx.Messages {
		if m.Role == "system" {
			sysParts = append(sysParts, m.Content)
		}
	}
	req.System = joinStrings(sysParts, "\n\n")

	var pending []AnthropicContent
	flush := func() {
		if len(pending) == 0 {
			return
		}
		req.Messages = append(req.Messages, AnthropicMessage{Role: "user", Content: pending})
		pending = nil
	}

	for _, m := range ctx.Messages {
		switch m.Role {
		case "system":
		case "user":
			flush()
			block := AnthropicContent{Type: "text", Text: m.Content}
			if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "user" {
				req.Messages[n-1].Content = append(req.Messages[n-1].Content, block)
			} else {
				req.Messages = append(req.Messages, AnthropicMessage{
					Role:    "user",
					Content: []AnthropicContent{block},
				})
			}
		case "assistant":
			flush()
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
			req.Messages = append(req.Messages, AnthropicMessage{Role: "assistant", Content: contents})
		case "tool":
			pending = append(pending, AnthropicContent{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
		}
	}
	flush()

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

func (c AnthropicCompiler) Compile(ctx domain.Context) (Messages, error) {
	if err := checkMessages(ctx.Messages); err != nil {
		return nil, err
	}
	return Messages(ctx.Messages), nil
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

var _ = json.Marshal
