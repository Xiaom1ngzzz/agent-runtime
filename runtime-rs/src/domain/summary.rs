//! Summary / Progress 结构 —— 与 `runtime-go/domain/summary.go` 对齐。
//!
//! 见 ch04 §4.6 (Summary) 与 §4.7 (Progress)。

use std::collections::HashMap;

use serde::{Deserialize, Serialize};

/// 一段 Turn 范围的结构化摘要。ch04 §4.6.1。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Summary {
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub task_id: String,
    #[serde(default)]
    pub from_seq: i64,
    #[serde(default)]
    pub to_seq: i64,

    #[serde(default)]
    pub user_intents: Vec<String>,
    /// key = "tool_name:key_arg"，value = 关键返回值(JSON 字符串)。
    #[serde(default)]
    pub tool_results: HashMap<String, String>,
    #[serde(default)]
    pub decisions_made: Vec<Decision>,
    #[serde(default)]
    pub open_questions: Vec<String>,
    #[serde(default)]
    pub next_actions: Vec<String>,

    #[serde(default)]
    pub model_used: String,
    #[serde(default)]
    pub prompt_version: String,
    #[serde(default)]
    pub confidence: f64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Decision {
    #[serde(default)]
    pub what: String,
    #[serde(default)]
    pub why: String,
    #[serde(default)]
    pub at_seq: i64,
}

/// 一个 Task 的进度快照。ch04 §4.7.2。
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct Progress {
    #[serde(default)]
    pub goal: String,
    #[serde(default)]
    pub done: Vec<Step>,
    #[serde(default)]
    pub next: Vec<Step>,
    #[serde(default)]
    pub open: Vec<OpenLoop>,
    #[serde(default)]
    pub version: i64,
    #[serde(default)]
    pub updated_at: String, // Event ID
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct Step {
    #[serde(default)]
    pub intent: String,
    #[serde(default)]
    pub action: String,
    #[serde(default)]
    pub observation: String,
    #[serde(default)]
    pub cost: f64,
    #[serde(default)]
    pub duration: i64,
    #[serde(default)]
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
    #[serde(default)]
    pub question: String,
    #[serde(default)]
    pub raised_at: String, // Event ID
    #[serde(default)]
    pub blocking_steps: Vec<i32>,
}

/// WorkingSet 里的一条项目。ch04 §4.4.1。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct TurnDigest {
    #[serde(default)]
    pub turn_id: String,
    #[serde(default)]
    pub task_id: String,
    #[serde(default)]
    pub index: i32,
    #[serde(default)]
    pub from_seq: i64,
    #[serde(default)]
    pub to_seq: i64,
    /// 若已被 ContextCompressed 覆盖，Assemble 时跳过原文。
    #[serde(default)]
    pub superseded: bool,
}

/// 从 Memory 层查回来的相关片段。ch05 展开。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct MemoryRef {
    #[serde(default)]
    pub source: String, // "vector:kb.docs" | "kv:facts" | ...
    #[serde(default)]
    pub key: String,
    #[serde(default)]
    pub content: String,
    #[serde(default)]
    pub score: f64,
    #[serde(default)]
    pub queried_at_seq: i64,
}
