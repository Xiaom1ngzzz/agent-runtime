//! Summary / Progress 结构 —— 与 `runtime-go/domain/summary.go` 对齐。
//!
//! 见 ch04 §4.6 (Summary) 与 §4.7 (Progress)。

use std::collections::HashMap;

use serde::{Deserialize, Serialize};

/// 一段 Turn 范围的结构化摘要。ch04 §4.6.1。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Summary {
    pub session_id: String,
    pub task_id: String,
    pub from_seq: i64,
    pub to_seq: i64,

    pub user_intents: Vec<String>,
    /// key = "tool_name:key_arg"，value = 关键返回值(JSON 字符串)。
    pub tool_results: HashMap<String, String>,
    pub decisions_made: Vec<Decision>,
    pub open_questions: Vec<String>,
    pub next_actions: Vec<String>,

    pub model_used: String,
    pub prompt_version: String,
    pub confidence: f64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Decision {
    pub what: String,
    pub why: String,
    pub at_seq: i64,
}

/// 一个 Task 的进度快照。ch04 §4.7.2。
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct Progress {
    pub goal: String,
    pub done: Vec<Step>,
    pub next: Vec<Step>,
    pub open: Vec<OpenLoop>,
    pub version: i64,
    pub updated_at: String, // Event ID
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct Step {
    pub intent: String,
    pub action: String,
    pub observation: String,
    pub cost: f64,
    pub duration: i64,
    pub kind: StepKind,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum StepKind {
    #[default]
    Decision,
    ToolCall,
    UserInput,
    Error,
    /// 可丢：只读探测，需要时可从 EventStore 重放。
    ReadOnly,
    /// 聚合节点：如"批量查 20 个订单"。
    Aggregated,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct OpenLoop {
    pub question: String,
    pub raised_at: String, // Event ID
    pub blocking_steps: Vec<i32>,
}

/// WorkingSet 里的一条项目。ch04 §4.4.1。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct TurnDigest {
    pub turn_id: String,
    pub task_id: String,
    pub index: i32,
    pub from_seq: i64,
    pub to_seq: i64,
    /// 若已被 ContextCompressed 覆盖，Assemble 时跳过原文。
    pub superseded: bool,
}

/// 从 Memory 层查回来的相关片段。ch05 展开。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct MemoryRef {
    pub source: String, // "vector:kb.docs" | "kv:facts" | ...
    pub key: String,
    pub content: String,
    pub score: f64,
    pub queried_at_seq: i64,
}
