//! LLMProvider：Runtime 之外的相邻系统，只声明协议。
//! 与 `runtime-go/llm/llm.go` 对齐。

use crate::domain::{LLMResponse, Tool};
use crate::prompt::Messages;

pub trait LLMProvider {
    fn chat(&self, msgs: &Messages, tools: &[Tool]) -> Result<LLMResponse, LLMError>;
}

#[derive(Debug)]
pub struct LLMError(pub String);
