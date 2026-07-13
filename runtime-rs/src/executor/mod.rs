//! Executor：驱动一个 Turn 完成——调 LLM、分发工具、回收结果、生成 Event 流。
//! 与 `runtime-go/executor/executor.go` 对齐。实现见 ch08-executor.md。

use crate::domain::{Event, Turn};

pub trait Executor {
    fn run(&self, turn: &Turn) -> Result<Vec<Event>, ExecutorError>;
}

#[derive(Debug)]
pub struct ExecutorError(pub String);
