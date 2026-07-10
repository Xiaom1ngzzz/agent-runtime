//! ch06 §6.10.2 端到端证据 —— Rust 版。与 `runtime-go/prompt/ch06_provider_diff_test.go` 对齐。

use agent_runtime_rs::domain::{Context, Message, Tool, ToolCall};
use agent_runtime_rs::prompt::{
    AnthropicCompiler, OpenAICompiler, PromptCompiler, ReferenceCompiler,
};

fn build_common_context() -> Context {
    Context {
        session_id: "s1".into(),
        task_id: "t1".into(),
        messages: vec![
            Message { role: "system".into(), content: "You are an agent.".into(), ..Default::default() },
            Message { role: "system".into(), content: "<task_frame>goal: 帮我订机票</task_frame>".into(), ..Default::default() },
            Message { role: "user".into(), content: "帮我订下周二北京到上海的机票".into(), ..Default::default() },
            Message {
                role: "assistant".into(),
                content: String::new(),
                tool_calls: vec![ToolCall {
                    id: "call_1".into(),
                    name: "search_flight".into(),
                    arguments: r#"{"from":"BJ","to":"SH","date":"2026-07-14"}"#.into(),
                }],
                ..Default::default()
            },
            Message {
                role: "tool".into(),
                tool_call_id: "call_1".into(),
                content: r#"{"flights":[{"no":"CA1509"}]}"#.into(),
                ..Default::default()
            },
            Message {
                role: "assistant".into(),
                content: "找到航班 CA1509,是否预订?".into(),
                ..Default::default()
            },
        ],
        tools: vec![Tool {
            name: "search_flight".into(),
            description: "搜索航班".into(),
            schema: r#"{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"},"date":{"type":"string"}}}"#.into(),
        }],
        turn_id: "".into(),
    }
}

#[test]
fn ch06_provider_diff_anthropic() {
    let ctx = build_common_context();
    let c = AnthropicCompiler { model: "claude-opus-4-7".into() };
    let req = c.compile_to_provider(&ctx).expect("compile");

    // 1. system 独立字段
    assert!(!req.system.is_empty(), "system field should be populated");
    // 2. messages 里没有 system
    for (i, m) in req.messages.iter().enumerate() {
        assert!(m.role != "system", "messages[{}] should not have role=system", i);
    }
    // 3. tool schema = input_schema
    assert_eq!(req.tools.len(), 1);
    assert!(!req.tools[0].input_schema.is_empty(), "input_schema should be set");

    // 4. tool_result 是 content 数组项
    let found_tool_result = req.messages.iter().any(|m| {
        m.content.iter().any(|c| c.kind == "tool_result" && c.tool_use_id == "call_1")
    });
    assert!(found_tool_result, "tool_result should appear as content array item");

    // 5. tool_use 是 assistant.content 数组项
    let found_tool_use = req.messages.iter().any(|m| {
        m.role == "assistant" && m.content.iter().any(|c| c.kind == "tool_use" && c.tool_use_id == "call_1")
    });
    assert!(found_tool_use, "tool_use should appear as content array item");
}

#[test]
fn ch06_provider_diff_openai() {
    let ctx = build_common_context();
    let c = OpenAICompiler { model: "gpt-5".into() };
    let req = c.compile_to_provider(&ctx).expect("compile");

    // 1. system 在 messages 数组里(有多条)
    let sys_count = req.messages.iter().filter(|m| m.role == "system").count();
    assert!(sys_count >= 2, "expected 2+ system messages, got {}", sys_count);

    // 2. tool.function.parameters
    assert_eq!(req.tools.len(), 1);
    assert!(!req.tools[0].function_parameters.is_empty(), "function_parameters should be set");
    assert_eq!(req.tools[0].kind, "function");

    // 3. assistant.tool_calls 是独立字段
    let found_tool_call = req.messages.iter().any(|m| {
        m.role == "assistant" && m.tool_calls.iter().any(|tc| tc.id == "call_1")
    });
    assert!(found_tool_call, "expected tool_call id=call_1 in assistant.tool_calls");

    // 4. tool response 是独立 role=tool
    let found_tool_response = req.messages.iter().any(|m| {
        m.role == "tool" && m.tool_call_id == "call_1"
    });
    assert!(found_tool_response, "expected role=tool with tool_call_id=call_1");
}

#[test]
fn ch06_type_check_catches_tool_call_id_mismatch() {
    let ctx = Context {
        messages: vec![
            Message { role: "system".into(), content: "You are an agent.".into(), ..Default::default() },
            Message { role: "user".into(), content: "hi".into(), ..Default::default() },
            // role=tool 但没有前置的 assistant.tool_calls
            Message {
                role: "tool".into(),
                tool_call_id: "orphan_call".into(),
                content: "result".into(),
                ..Default::default()
            },
        ],
        ..Default::default()
    };
    let err = ReferenceCompiler.compile(&ctx);
    assert!(err.is_err(), "expected PromptError for orphan tool_call_id");
    let msg = err.unwrap_err().0;
    assert!(msg.contains("tool_call_id"), "error should mention tool_call_id: {}", msg);
}

#[test]
fn ch06_anthropic_rejects_consecutive_user() {
    let ctx = Context {
        messages: vec![
            Message { role: "user".into(), content: "hi 1".into(), ..Default::default() },
            Message { role: "user".into(), content: "hi 2".into(), ..Default::default() },
        ],
        ..Default::default()
    };
    let c = AnthropicCompiler { model: "claude-opus-4-7".into() };
    assert!(c.compile_to_provider(&ctx).is_err(),
            "expected error for consecutive user in Anthropic");

    let oc = OpenAICompiler { model: "gpt-5".into() };
    assert!(oc.compile_to_provider(&ctx).is_ok(),
            "openai should accept consecutive user");
}
