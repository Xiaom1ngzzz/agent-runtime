//! ContextEngine:把 Fold 后的 SessionView 投影成一次 Turn 需要的 Context。
//! 实现可从 EventStore 只读展开消息原文(见 ADR-002、ch03 §3.5.1、ch04 §4.4)。

use crate::domain::Context;

pub trait ContextEngine {
    fn assemble(&self, session_id: &str, task_id: &str) -> Result<Context, ContextError>;
}

#[derive(Debug)]
pub struct ContextError(pub String);
