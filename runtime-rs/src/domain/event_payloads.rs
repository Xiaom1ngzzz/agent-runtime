//! Event Payload 的类型系统。
//!
//! Go 版用 marker interface 收紧 `Event.Payload`；Rust 天然的对应物是
//! 一个封闭的 enum —— 编译器强制穷举，比 marker trait 更严格。
//!
//! 每个 variant 对应 Go 版一个 EventType，字段与 Go 端 Payload* struct 对齐。

use serde::{Deserialize, Serialize};

use super::summary::{MemoryRef, Progress, Summary};
use super::{Budget, Message, TaskStatus, Tool, ToolCall, TurnStatus};

/// EventType 与 Payload 的 discriminant 合并成一个类型。
/// 判别一个 Event 的类型 = match 它的 Payload。
pub type EventType = &'static str;

pub const EVT_SESSION_OPENED: EventType = "SessionOpened";
pub const EVT_TASK_CREATED: EventType = "TaskCreated";
pub const EVT_TASK_ENDED: EventType = "TaskEnded";
pub const EVT_TURN_STARTED: EventType = "TurnStarted";
pub const EVT_TURN_ENDED: EventType = "TurnEnded";
pub const EVT_USER_SPOKE: EventType = "UserSpoke";
pub const EVT_LLM_REQUESTED: EventType = "LLMRequested";
pub const EVT_LLM_REPLIED: EventType = "LLMReplied";
pub const EVT_TOOL_CALLED: EventType = "ToolCalled";
pub const EVT_TOOL_RETURNED: EventType = "ToolReturned";
pub const EVT_CONTEXT_COMPRESSED: EventType = "ContextCompressed";
pub const EVT_COMPRESSION_SKIPPED: EventType = "CompressionSkipped";
pub const EVT_PROGRESS_UPDATED: EventType = "ProgressUpdated";
pub const EVT_MEMORY_QUERIED: EventType = "MemoryQueried";
pub const EVT_SUB_TASK_SPAWNED: EventType = "SubTaskSpawned";
pub const EVT_TOOL_BIND_FAILED: EventType = "ToolBindFailed";

/// 与 Go 版 `EventPayload` marker interface 对等。
/// 在 Rust 里用封闭 enum：外部无法新增 variant，消费方 match 必须穷举。
///
/// `#[serde(tag="type", content="payload")]` 让 wire format 与 Go 端 `EventDTO`
/// 一致:`{"type":"UserSpoke","payload":{...}}`。见 ch03 §3.3.2。
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", content = "payload")]
pub enum EventPayload {
    SessionOpened(PayloadSessionOpened),
    TaskCreated(PayloadTaskCreated),
    TaskEnded(PayloadTaskEnded),
    TurnStarted(PayloadTurnStarted),
    TurnEnded(PayloadTurnEnded),
    UserSpoke(PayloadUserSpoke),
    LLMRequested(PayloadLLMRequested),
    LLMReplied(PayloadLLMReplied),
    ToolCalled(PayloadToolCalled),
    ToolReturned(PayloadToolReturned),
    ContextCompressed(PayloadContextCompressed),
    CompressionSkipped(PayloadCompressionSkipped),
    ProgressUpdated(PayloadProgressUpdated),
    MemoryQueried(PayloadMemoryQueried),
    SubTaskSpawned(PayloadSubTaskSpawned),
    ToolBindFailed(PayloadToolBindFailed),
}

impl EventPayload {
    pub fn event_type(&self) -> EventType {
        match self {
            EventPayload::SessionOpened(_) => EVT_SESSION_OPENED,
            EventPayload::TaskCreated(_) => EVT_TASK_CREATED,
            EventPayload::TaskEnded(_) => EVT_TASK_ENDED,
            EventPayload::TurnStarted(_) => EVT_TURN_STARTED,
            EventPayload::TurnEnded(_) => EVT_TURN_ENDED,
            EventPayload::UserSpoke(_) => EVT_USER_SPOKE,
            EventPayload::LLMRequested(_) => EVT_LLM_REQUESTED,
            EventPayload::LLMReplied(_) => EVT_LLM_REPLIED,
            EventPayload::ToolCalled(_) => EVT_TOOL_CALLED,
            EventPayload::ToolReturned(_) => EVT_TOOL_RETURNED,
            EventPayload::ContextCompressed(_) => EVT_CONTEXT_COMPRESSED,
            EventPayload::CompressionSkipped(_) => EVT_COMPRESSION_SKIPPED,
            EventPayload::ProgressUpdated(_) => EVT_PROGRESS_UPDATED,
            EventPayload::MemoryQueried(_) => EVT_MEMORY_QUERIED,
            EventPayload::SubTaskSpawned(_) => EVT_SUB_TASK_SPAWNED,
            EventPayload::ToolBindFailed(_) => EVT_TOOL_BIND_FAILED,
        }
    }
}

// ---------- 具体 payload 结构 ----------

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadSessionOpened {
    #[serde(default)]
    pub principal: String,
    #[serde(default)]
    pub metadata: std::collections::HashMap<String, String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadTaskCreated {
    #[serde(default)]
    pub goal: String,
    #[serde(default)]
    pub budget: Budget,
    #[serde(default)]
    pub parent_id: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadTaskEnded {
    #[serde(default)]
    pub status: TaskStatus,
    #[serde(default)]
    pub reason: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadTurnStarted {
    #[serde(default)]
    pub index: i32,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadTurnEnded {
    #[serde(default)]
    pub status: TurnStatus,
    #[serde(default)]
    pub tokens_in: i64,
    #[serde(default)]
    pub tokens_out: i64,
    #[serde(default)]
    pub cost_us: f64,
    #[serde(default)]
    pub latency_ms: i64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadUserSpoke {
    #[serde(default)]
    pub text: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadLLMRequested {
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub messages: Vec<Message>,
    #[serde(default)]
    pub tools: Vec<Tool>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadLLMReplied {
    #[serde(default)]
    pub assistant: Message,
    #[serde(default)]
    pub tool_calls: Vec<ToolCall>,
    #[serde(default)]
    pub tokens_in: i64,
    #[serde(default)]
    pub tokens_out: i64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadToolCalled {
    #[serde(default)]
    pub call_id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub arguments: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadToolReturned {
    #[serde(default)]
    pub call_id: String,
    #[serde(default)]
    pub content: String,
    #[serde(default)]
    pub is_error: bool,
}

/// 与 Go 版 `PayloadContextCompressed` 对齐。见 ch04 §4.5.3。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadContextCompressed {
    #[serde(default)]
    pub from_seq: i64,
    #[serde(default)]
    pub to_seq: i64,
    #[serde(default)]
    pub strategy: String,
    #[serde(default)]
    pub summary: Summary,
    #[serde(default)]
    pub from_tokens: i64,
    #[serde(default)]
    pub to_tokens: i64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadCompressionSkipped {
    #[serde(default)]
    pub reason: String,
    #[serde(default)]
    pub detail: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadProgressUpdated {
    #[serde(default)]
    pub task_id: String,
    #[serde(default)]
    pub progress: Progress,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadMemoryQueried {
    #[serde(default)]
    pub query: String,
    #[serde(default)]
    pub refs: Vec<MemoryRef>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadSubTaskSpawned {
    #[serde(default)]
    pub parent_task_id: String,
    #[serde(default)]
    pub child_task_id: String,
    #[serde(default)]
    pub goal: String,
    #[serde(default)]
    pub budget: Budget,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PayloadToolBindFailed {
    #[serde(default)]
    pub call_id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub reason: String,
}
