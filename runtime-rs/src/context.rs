//! ContextEngine：把 State 中相关的 Event 流投影成一次 Turn 需要的消息序列。
//! 与 `runtime-go/context/context.go` 对齐。实现见 ch04-context-engine.md。

use crate::domain::Context;

pub trait ContextEngine {
    fn assemble(&self, session_id: &str, task_id: &str) -> Result<Context, ContextError>;
}

#[derive(Debug)]
pub struct ContextError(pub String);
