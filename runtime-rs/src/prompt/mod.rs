//! PromptCompiler：把结构化的 Context 转成 LLM 能吃的 Messages。
//! 与 `runtime-go/prompt/` 对齐。
//!
//! ch06 §6.2 定义了 Compile 的四段职责:Layout / Type-check / Optimize / Emit。
//! 本模块提供:
//!   - `PromptCompiler` trait(通用接口,只返回 Messages)
//!   - `ReferenceCompiler` / `AnthropicCompiler` / `OpenAICompiler`(三档实现)
//!   - Provider-specific 请求体类型 + `compile_to_anthropic` / `compile_to_openai` 显式 API
//!   - `PromptError` 携带失败字段(§6.4 反例正解)

use crate::domain::{Context, Message};

pub type Messages = Vec<Message>;

pub trait PromptCompiler {
    fn compile(&self, ctx: &Context) -> Result<Messages, PromptError>;
}

#[derive(Debug)]
pub struct PromptError(pub String);

// ---------- ReferenceCompiler ----------

pub struct ReferenceCompiler;

impl PromptCompiler for ReferenceCompiler {
    fn compile(&self, ctx: &Context) -> Result<Messages, PromptError> {
        check_messages(&ctx.messages)?;
        Ok(ctx.messages.clone())
    }
}

// ---------- Anthropic ----------

/// 近似 Anthropic Messages API 的请求体。
#[derive(Debug, Clone, Default)]
pub struct AnthropicRequest {
    pub model: String,
    pub system: String,
    pub messages: Vec<AnthropicMessage>,
    pub tools: Vec<AnthropicTool>,
}

#[derive(Debug, Clone, Default)]
pub struct AnthropicMessage {
    pub role: String,
    pub content: Vec<AnthropicContent>,
}

#[derive(Debug, Clone, Default)]
pub struct AnthropicContent {
    pub kind: String,
    pub text: String,
    pub tool_use_id: String,
    pub tool_name: String,
    pub tool_input: String,
    pub content: String,
}

#[derive(Debug, Clone, Default)]
pub struct AnthropicTool {
    pub name: String,
    pub description: String,
    pub input_schema: String,
}

pub struct AnthropicCompiler {
    pub model: String,
}

impl PromptCompiler for AnthropicCompiler {
    fn compile(&self, ctx: &Context) -> Result<Messages, PromptError> {
        check_messages(&ctx.messages)?;
        Ok(ctx.messages.clone())
    }
}

impl AnthropicCompiler {
    pub fn compile_to_provider(&self, ctx: &Context) -> Result<AnthropicRequest, PromptError> {
        check_messages(&ctx.messages)?;

        let mut req = AnthropicRequest {
            model: self.model.clone(),
            ..Default::default()
        };

        let sys_parts: Vec<&str> = ctx
            .messages
            .iter()
            .filter(|m| m.role == "system")
            .map(|m| m.content.as_str())
            .collect();
        req.system = sys_parts.join("\n\n");

        let mut pending_tool_results: Vec<AnthropicContent> = Vec::new();
        let flush_tool_results =
            |req: &mut AnthropicRequest, pending: &mut Vec<AnthropicContent>| {
                if pending.is_empty() {
                    return;
                }
                req.messages.push(AnthropicMessage {
                    role: "user".into(),
                    content: std::mem::take(pending),
                });
            };

        for m in &ctx.messages {
            match m.role.as_str() {
                "system" => {}
                "user" => {
                    flush_tool_results(&mut req, &mut pending_tool_results);
                    let block = AnthropicContent {
                        kind: "text".into(),
                        text: m.content.clone(),
                        ..Default::default()
                    };
                    if let Some(last) = req.messages.last_mut() {
                        if last.role == "user" {
                            last.content.push(block);
                            continue;
                        }
                    }
                    req.messages.push(AnthropicMessage {
                        role: "user".into(),
                        content: vec![block],
                    });
                }
                "assistant" => {
                    flush_tool_results(&mut req, &mut pending_tool_results);
                    let mut contents = Vec::new();
                    if !m.content.is_empty() {
                        contents.push(AnthropicContent {
                            kind: "text".into(),
                            text: m.content.clone(),
                            ..Default::default()
                        });
                    }
                    for tc in &m.tool_calls {
                        contents.push(AnthropicContent {
                            kind: "tool_use".into(),
                            tool_use_id: tc.id.clone(),
                            tool_name: tc.name.clone(),
                            tool_input: tc.arguments.clone(),
                            ..Default::default()
                        });
                    }
                    req.messages.push(AnthropicMessage {
                        role: "assistant".into(),
                        content: contents,
                    });
                }
                "tool" => {
                    pending_tool_results.push(AnthropicContent {
                        kind: "tool_result".into(),
                        tool_use_id: m.tool_call_id.clone(),
                        content: m.content.clone(),
                        ..Default::default()
                    });
                }
                _ => {}
            }
        }
        flush_tool_results(&mut req, &mut pending_tool_results);

        for t in &ctx.tools {
            let schema = if t.schema.is_empty() {
                r#"{"type":"object"}"#.into()
            } else {
                t.schema.clone()
            };
            req.tools.push(AnthropicTool {
                name: t.name.clone(),
                description: t.description.clone(),
                input_schema: schema,
            });
        }

        Ok(req)
    }
}

// ---------- OpenAI ----------

#[derive(Debug, Clone, Default)]
pub struct OpenAIRequest {
    pub model: String,
    pub messages: Vec<OpenAIMessage>,
    pub tools: Vec<OpenAITool>,
}

#[derive(Debug, Clone, Default)]
pub struct OpenAIMessage {
    pub role: String,
    pub content: String,
    pub tool_calls: Vec<OpenAIToolCall>,
    pub tool_call_id: String,
}

#[derive(Debug, Clone, Default)]
pub struct OpenAIToolCall {
    pub id: String,
    pub kind: String,
    pub function_name: String,
    pub function_arguments: String,
}

#[derive(Debug, Clone, Default)]
pub struct OpenAITool {
    pub kind: String,
    pub function_name: String,
    pub function_description: String,
    pub function_parameters: String,
}

pub struct OpenAICompiler {
    pub model: String,
}

impl PromptCompiler for OpenAICompiler {
    fn compile(&self, ctx: &Context) -> Result<Messages, PromptError> {
        check_messages(&ctx.messages)?;
        Ok(ctx.messages.clone())
    }
}

impl OpenAICompiler {
    pub fn compile_to_provider(&self, ctx: &Context) -> Result<OpenAIRequest, PromptError> {
        check_messages(&ctx.messages)?;

        let mut req = OpenAIRequest {
            model: self.model.clone(),
            ..Default::default()
        };

        for m in &ctx.messages {
            match m.role.as_str() {
                "system" => req.messages.push(OpenAIMessage {
                    role: "system".into(),
                    content: m.content.clone(),
                    ..Default::default()
                }),
                "user" => req.messages.push(OpenAIMessage {
                    role: "user".into(),
                    content: m.content.clone(),
                    ..Default::default()
                }),
                "assistant" => {
                    let mut om = OpenAIMessage {
                        role: "assistant".into(),
                        content: m.content.clone(),
                        ..Default::default()
                    };
                    for tc in &m.tool_calls {
                        om.tool_calls.push(OpenAIToolCall {
                            id: tc.id.clone(),
                            kind: "function".into(),
                            function_name: tc.name.clone(),
                            function_arguments: tc.arguments.clone(),
                        });
                    }
                    req.messages.push(om);
                }
                "tool" => req.messages.push(OpenAIMessage {
                    role: "tool".into(),
                    content: m.content.clone(),
                    tool_call_id: m.tool_call_id.clone(),
                    ..Default::default()
                }),
                _ => {}
            }
        }

        for t in &ctx.tools {
            let params = if t.schema.is_empty() {
                r#"{"type":"object"}"#.into()
            } else {
                t.schema.clone()
            };
            req.tools.push(OpenAITool {
                kind: "function".into(),
                function_name: t.name.clone(),
                function_description: t.description.clone(),
                function_parameters: params,
            });
        }

        Ok(req)
    }
}

// ---------- Type-check ----------

/// §6.4 基线校验。违反 → PromptError 带具体字段。
pub fn check_messages(msgs: &[Message]) -> Result<(), PromptError> {
    for (i, m) in msgs.iter().enumerate() {
        match m.role.as_str() {
            "system" | "user" | "assistant" | "tool" => {}
            other => {
                return Err(PromptError(format!(
                    "prompt check failed at message[{}].role: unknown role: {}",
                    i, other
                )));
            }
        }
    }
    for (i, m) in msgs.iter().enumerate() {
        if m.role != "tool" {
            continue;
        }
        if m.tool_call_id.is_empty() {
            return Err(PromptError(format!(
                "prompt check failed at message[{}].tool_call_id: role=tool without tool_call_id",
                i
            )));
        }
        let mut matched = false;
        for j in (0..i).rev() {
            if msgs[j].role == "assistant" {
                for tc in &msgs[j].tool_calls {
                    if tc.id == m.tool_call_id {
                        matched = true;
                    }
                }
                break;
            }
        }
        if !matched {
            return Err(PromptError(format!(
                "prompt check failed at message[{}].tool_call_id: {} has no matching assistant.tool_calls[]",
                i, m.tool_call_id
            )));
        }
    }
    Ok(())
}

pub fn check_no_consecutive_user(msgs: &[Message]) -> Result<(), PromptError> {
    let mut prev = "";
    for (i, m) in msgs.iter().enumerate() {
        if m.role == "user" && prev == "user" {
            return Err(PromptError(format!(
                "prompt check failed at message[{}].role: anthropic: consecutive user messages not allowed",
                i
            )));
        }
        prev = &m.role;
    }
    Ok(())
}
