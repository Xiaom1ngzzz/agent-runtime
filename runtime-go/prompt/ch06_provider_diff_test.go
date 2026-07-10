package prompt_test

// TestCh06ProviderDiff 是 ch06 §6.10.2 承诺的端到端证据。
//
// 同一份 Context(含 system + user + assistant with tool_calls + tool response + tools list),
// 用 Anthropic 与 OpenAI 两个 Compiler 编译,断言:
//   - Anthropic:system 独立字段,不在 messages 数组;tool schema key = input_schema
//   - OpenAI:system 是 messages[0];tool schema key = parameters
//   - 两者对同一 Context 都能通过 Type-check
//
// TestCh06TypeCheckCatchesToolCallIDMismatch 独立断言 §6.4 的 Type-check 有效。

import (
	"testing"

	"agent-runtime-go/domain"
	"agent-runtime-go/prompt"
)

func buildCommonContext() domain.Context {
	return domain.Context{
		SessionID: "s1",
		TaskID:    "t1",
		Messages: []domain.Message{
			{Role: "system", Content: "You are an agent."},
			{Role: "system", Content: "<task_frame>goal: 帮我订机票</task_frame>"},
			{Role: "user", Content: "帮我订下周二北京到上海的机票"},
			{Role: "assistant", Content: "",
				ToolCalls: []domain.ToolCall{{
					ID: "call_1", Name: "search_flight",
					Arguments: `{"from":"BJ","to":"SH","date":"2026-07-14"}`,
				}}},
			{Role: "tool", ToolCallID: "call_1", Content: `{"flights":[{"no":"CA1509"}]}`},
			{Role: "assistant", Content: "找到航班 CA1509,是否预订?"},
		},
		Tools: []domain.Tool{
			{
				Name: "search_flight", Description: "搜索航班",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from": map[string]any{"type": "string"},
						"to":   map[string]any{"type": "string"},
						"date": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

func TestCh06ProviderDiff_Anthropic(t *testing.T) {
	ctx := buildCommonContext()
	c := prompt.AnthropicCompiler{Model: "claude-opus-4-7"}
	req, err := c.CompileToProvider(ctx)
	if err != nil {
		t.Fatalf("anthropic compile: %v", err)
	}

	// 1. system 独立,不在 messages
	if req.System == "" {
		t.Fatal("anthropic: system field should be populated")
	}
	for i, m := range req.Messages {
		if m.Role == "system" {
			t.Fatalf("anthropic: messages[%d] should not have role=system", i)
		}
	}

	// 2. tool schema key = input_schema
	if len(req.Tools) != 1 {
		t.Fatalf("anthropic: expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].InputSchema == nil {
		t.Fatal("anthropic: tool.InputSchema should be set (key = input_schema)")
	}

	// 3. tool_result 作为 user message 的 content 数组项
	foundToolResult := false
	for _, m := range req.Messages {
		for _, ct := range m.Content {
			if ct.Type == "tool_result" && ct.ToolUseID == "call_1" {
				foundToolResult = true
			}
		}
	}
	if !foundToolResult {
		t.Fatal("anthropic: tool_result should appear as content array item")
	}

	// 4. assistant with tool_calls 应该展开为 content 数组的 tool_use 项
	foundToolUse := false
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, ct := range m.Content {
			if ct.Type == "tool_use" && ct.ToolUseID == "call_1" {
				foundToolUse = true
			}
		}
	}
	if !foundToolUse {
		t.Fatal("anthropic: tool_use should appear as content array item")
	}
}

func TestCh06ProviderDiff_OpenAI(t *testing.T) {
	ctx := buildCommonContext()
	c := prompt.OpenAICompiler{Model: "gpt-5"}
	req, err := c.CompileToProvider(ctx)
	if err != nil {
		t.Fatalf("openai compile: %v", err)
	}

	// 1. system 在 messages[0..N],不独立
	sysCount := 0
	for _, m := range req.Messages {
		if m.Role == "system" {
			sysCount++
		}
	}
	if sysCount < 2 {
		t.Fatalf("openai: expected 2 system messages (Instructions + TaskFrame), got %d", sysCount)
	}

	// 2. tool schema key = parameters
	if len(req.Tools) != 1 {
		t.Fatalf("openai: expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Function.Parameters == nil {
		t.Fatal("openai: tool.Function.Parameters should be set (key = parameters)")
	}
	if req.Tools[0].Type != "function" {
		t.Fatal("openai: tool.Type should be 'function'")
	}

	// 3. assistant.tool_calls 是独立字段
	foundToolCall := false
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == "call_1" {
				foundToolCall = true
			}
		}
	}
	if !foundToolCall {
		t.Fatal("openai: expected tool_call with id=call_1 in assistant.tool_calls")
	}

	// 4. tool response 是独立 role=tool 的 message
	foundToolResponse := false
	for _, m := range req.Messages {
		if m.Role == "tool" && m.ToolCallID == "call_1" {
			foundToolResponse = true
		}
	}
	if !foundToolResponse {
		t.Fatal("openai: expected role=tool message with tool_call_id=call_1")
	}
}

func TestCh06TypeCheckCatchesToolCallIDMismatch(t *testing.T) {
	ctx := domain.Context{
		Messages: []domain.Message{
			{Role: "system", Content: "You are an agent."},
			{Role: "user", Content: "hi"},
			// role=tool 但没有前置的 assistant.tool_calls[]
			{Role: "tool", ToolCallID: "orphan_call", Content: "result"},
		},
	}
	ref := prompt.ReferenceCompiler{}
	_, err := ref.Compile(ctx)
	if err == nil {
		t.Fatal("expected PromptCheckError for orphan tool_call_id")
	}
	if perr, ok := err.(*prompt.PromptCheckError); ok {
		if perr.Field != "tool_call_id" {
			t.Fatalf("expected error on field tool_call_id, got %s", perr.Field)
		}
	} else {
		t.Fatalf("expected *PromptCheckError, got %T", err)
	}
}

func TestCh06AnthropicRejectsConsecutiveUser(t *testing.T) {
	ctx := domain.Context{
		Messages: []domain.Message{
			{Role: "user", Content: "hi 1"},
			{Role: "user", Content: "hi 2"},
		},
	}
	c := prompt.AnthropicCompiler{Model: "claude-opus-4-7"}
	_, err := c.CompileToProvider(ctx)
	if err == nil {
		t.Fatal("expected error for consecutive user in Anthropic")
	}
	// OpenAI 版本应该通过
	oc := prompt.OpenAICompiler{Model: "gpt-5"}
	if _, err := oc.CompileToProvider(ctx); err != nil {
		t.Fatalf("openai should accept consecutive user: %v", err)
	}
}
