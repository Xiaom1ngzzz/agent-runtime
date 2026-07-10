//! PromptCompiler：把结构化的 Context 转成 LLM 能吃的 Messages。
//! 与 `runtime-go/prompt/prompt.go` 对齐。

use crate::domain::{Context, Message};

pub type Messages = Vec<Message>;

pub trait PromptCompiler {
    fn compile(&self, ctx: &Context) -> Result<Messages, PromptError>;
}

#[derive(Debug)]
pub struct PromptError(pub String);
