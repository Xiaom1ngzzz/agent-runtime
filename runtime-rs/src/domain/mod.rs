//! Runtime 世界里的四层核心对象与共享类型。
//! 与 `runtime-go/domain/domain.go` 逐字段对齐。
//! 只包含数据结构，不含行为——行为在各操作模块中。

pub mod event_payloads;
pub mod summary;
pub mod task_graph;

pub use event_payloads::*;
pub use summary::*;
pub use task_graph::*;

use serde::{Deserialize, Serialize};

use std::collections::HashMap;
use std::time::SystemTime;

// ---------- 三层聚合 ----------

/// 用户与系统的一段会话周期。
#[derive(Debug, Clone, Default)]
pub struct Session {
    pub id: String,
    pub principal: String, // 谁的 session
    pub created_at: Option<SystemTime>,
    pub last_active_at: Option<SystemTime>,
    pub metadata: HashMap<String, String>,
}

/// Session 内一件具体的事情。
/// 是取消/重试/超时/成败评估的自然单位。
/// `parent_id` 非空时表示嵌套子 Task(ch07 Task Graph)。
#[derive(Debug, Clone, Default)]
pub struct Task {
    pub id: String,
    pub session_id: String,
    pub parent_id: String, // 空 = 根 Task
    pub goal: String,
    pub status: TaskStatus,
    pub budget: Budget,
    pub started_at: Option<SystemTime>,
    pub ended_at: Option<SystemTime>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum TaskStatus {
    #[default]
    Pending,
    Running,
    Succeeded,
    Failed,
    Canceled,
    Timeout,
}

/// Task 允许消耗的资源上限。
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize)]
pub struct Budget {
    pub max_tokens: i64,
    pub max_cost_us: f64,
    pub max_wall_ms: i64,
}

/// Runtime 与 LLM 的一次完整往返，
/// 覆盖 LLM 调用 + 由此触发的所有工具调用 + 状态更新。
#[derive(Debug, Clone, Default)]
pub struct Turn {
    pub id: String,
    pub session_id: String,
    pub task_id: String,
    pub index: i32,
    pub status: TurnStatus,
    /// TurnStarted 事件的 seq；TurnEnded 时用于 WorkingSet.from_seq。
    pub start_seq: i64,
    pub tokens_in: i64,
    pub tokens_out: i64,
    pub cost_us: f64,
    pub latency_ms: i64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum TurnStatus {
    #[default]
    Running,
    Done,
    Failed,
}

// ---------- 原子事实 ----------

/// Runtime 中一次原子的状态变化的不可变记录。
/// 追加式；State 是 Event 流的折叠结果。
#[derive(Debug, Clone)]
pub struct Event {
    pub id: String,
    pub session_id: String,
    pub task_id: String, // 空串表示不归属任何 Task
    pub turn_id: String, // 同上
    pub ts: Option<SystemTime>,
    pub caused_by: String, // 上游 Event id，构成因果链
    pub payload: EventPayload,
    /// 每 session 单调递增。由 EventStore 在 append 时分配。0 表示尚未分配。
    pub seq: i64,
}

// ---------- LLM 交互类型 ----------

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Message {
    #[serde(default)]
    pub role: String, // "system" | "user" | "assistant" | "tool"
    #[serde(default)]
    pub content: String,
    #[serde(default)]
    pub tool_calls: Vec<ToolCall>,
    #[serde(default)]
    pub tool_call_id: String, // 仅当 role == "tool"
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ToolCall {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub arguments: String, // JSON 字符串
}

#[derive(Debug, Clone, Default)]
pub struct ToolResult {
    pub call_id: String,
    pub content: String,
    pub is_error: bool,
}

#[derive(Debug, Clone, Default)]
pub struct LLMResponse {
    pub assistant: Message,
    pub tool_calls: Vec<ToolCall>,
    pub tokens_in: i64,
    pub tokens_out: i64,
}

// ---------- 工具描述 ----------

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Tool {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub schema: String, // JSON Schema 文本；具体解析在 tool crate
}

// ---------- 上下文与视图 ----------

#[derive(Debug, Clone, Default)]
pub struct Context {
    pub session_id: String,
    pub task_id: String,
    pub turn_id: String,
    pub messages: Vec<Message>,
    pub tools: Vec<Tool>,
}

/// 从 Event 流折叠出的只读快照。
#[derive(Debug, Clone, Default)]
pub struct SessionView {
    pub session: Session,
    pub tasks: HashMap<String, Task>,
    pub last_turn: HashMap<String, Turn>,
    /// 此 View 已折叠到的最大 seq；用于 §3.5.4 单调校验与 §3.6 Snapshot。
    pub max_seq: i64,
    /// 此 View 已见过的所有 Event.id；用于 caused_by 因果链校验。
    pub seen_ids: std::collections::HashSet<String>,

    // ---------- ch04 Context 相关字段 ----------
    /// 最近几个 Turn 的原文引用。ch04 §4.4.1。
    pub working_set: Vec<TurnDigest>,
    /// 已生成的所有结构化摘要。key = seq 起点。
    pub summaries: HashMap<i64, Summary>,
    /// 跨 Session 检索出的相关片段(ch05 展开)。
    pub memory_refs: Vec<MemoryRef>,
    /// 每个 Task 的进度快照。ch04 §4.7。
    pub progresses: HashMap<String, Progress>,
}
