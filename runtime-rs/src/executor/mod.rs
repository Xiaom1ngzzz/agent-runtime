//! Executor —— 与 `runtime-go/executor/executor.go` 对齐。见 ch08-executor.md。

use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use crate::domain::{
    Event, EventPayload, PayloadToolBindFailed, PayloadToolCalled, PayloadToolReturned, Tool,
    ToolCall, Turn,
};
use crate::state::EventStore;

#[derive(Debug)]
pub struct ExecutorError(pub String);

pub trait Executor {
    fn run(&self, turn: &Turn) -> Result<Vec<Event>, ExecutorError>;
}

pub type ToolFn = Arc<dyn Fn(&str) -> Result<String, String> + Send + Sync>;

pub struct Registry {
    funcs: Mutex<HashMap<String, ToolFn>>,
    descs: Mutex<HashMap<String, Tool>>,
}

impl Registry {
    pub fn new() -> Self {
        Self {
            funcs: Mutex::new(HashMap::new()),
            descs: Mutex::new(HashMap::new()),
        }
    }

    pub fn register(&self, desc: Tool, fn_: ToolFn) {
        self.descs
            .lock()
            .unwrap()
            .insert(desc.name.clone(), desc.clone());
        self.funcs.lock().unwrap().insert(desc.name, fn_);
    }

    pub fn lookup(&self, name: &str) -> Option<ToolFn> {
        self.funcs.lock().unwrap().get(name).cloned()
    }

    pub fn descriptions(&self) -> Vec<Tool> {
        let mut out: Vec<_> = self.descs.lock().unwrap().values().cloned().collect();
        out.sort_by(|a, b| a.name.cmp(&b.name));
        out
    }
}

impl Default for Registry {
    fn default() -> Self {
        Self::new()
    }
}

/// 能暴露全量事件快照的 EventStore(mem fake)。
pub trait SnapshotStore: EventStore {
    fn snapshot_all(&self) -> Vec<Event>;
}

pub struct ToolExecutor<S: SnapshotStore> {
    pub store: Arc<Mutex<S>>,
    pub registry: Arc<Registry>,
    pub timeout: Option<Duration>,
}

impl<S: SnapshotStore> ToolExecutor<S> {
    pub fn new(store: Arc<Mutex<S>>, registry: Arc<Registry>) -> Self {
        Self {
            store,
            registry,
            timeout: Some(Duration::from_secs(5)),
        }
    }

    fn load_tool_calls(&self, turn: &Turn) -> Result<Vec<ToolCall>, ExecutorError> {
        let all = self.store.lock().unwrap().snapshot_all();
        for e in all.iter().rev() {
            if e.turn_id != turn.id {
                continue;
            }
            if !turn.session_id.is_empty() && e.session_id != turn.session_id {
                continue;
            }
            if !turn.task_id.is_empty() && e.task_id != turn.task_id {
                continue;
            }
            if let EventPayload::LLMReplied(p) = &e.payload {
                return Ok(p.tool_calls.clone());
            }
        }
        Err(ExecutorError("no LLMReplied in current turn".into()))
    }

    fn truncate_output(s: &str) -> String {
        const MAX: usize = 64 * 1024;
        if s.len() <= MAX {
            return s.to_string();
        }
        format!("{}…[truncated]", &s[..MAX])
    }

    fn dispatch_one(&self, call: &ToolCall) -> Vec<Event> {
        let called = Event {
            id: String::new(),
            session_id: String::new(),
            task_id: String::new(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::ToolCalled(PayloadToolCalled {
                call_id: call.id.clone(),
                name: call.name.clone(),
                arguments: call.arguments.clone(),
            }),
            seq: 0,
        };
        let Some(fn_) = self.registry.lookup(&call.name) else {
            return vec![
                called,
                Event {
                    id: String::new(),
                    session_id: String::new(),
                    task_id: String::new(),
                    turn_id: String::new(),
                    ts: None,
                    caused_by: String::new(),
                    payload: EventPayload::ToolBindFailed(PayloadToolBindFailed {
                        call_id: call.id.clone(),
                        name: call.name.clone(),
                        reason: "unknown_tool".into(),
                    }),
                    seq: 0,
                },
                Event {
                    id: String::new(),
                    session_id: String::new(),
                    task_id: String::new(),
                    turn_id: String::new(),
                    ts: None,
                    caused_by: String::new(),
                    payload: EventPayload::ToolReturned(PayloadToolReturned {
                        call_id: call.id.clone(),
                        content: format!("unknown tool: {}", call.name),
                        is_error: true,
                    }),
                    seq: 0,
                },
            ];
        };
        // Round 2:同步调用;timeout 由调用方在 ToolFn 内自行尊重(测试用 sleep+channel 模拟)。
        let returned = match fn_(&call.arguments) {
            Ok(content) => PayloadToolReturned {
                call_id: call.id.clone(),
                content: Self::truncate_output(&content),
                is_error: false,
            },
            Err(err) => PayloadToolReturned {
                call_id: call.id.clone(),
                content: err,
                is_error: true,
            },
        };
        vec![
            called,
            Event {
                id: String::new(),
                session_id: String::new(),
                task_id: String::new(),
                turn_id: String::new(),
                ts: None,
                caused_by: String::new(),
                payload: EventPayload::ToolReturned(returned),
                seq: 0,
            },
        ]
    }
}

impl<S: SnapshotStore + Send> Executor for ToolExecutor<S> {
    fn run(&self, turn: &Turn) -> Result<Vec<Event>, ExecutorError> {
        let calls = self.load_tool_calls(turn)?;
        let mut out = Vec::with_capacity(calls.len() * 2);
        for call in &calls {
            out.extend(self.dispatch_one(call));
        }
        Ok(out)
    }
}
