//! 第 2 章 example 用的最小内存 fake。
//! 与 `runtime-go/runtime/memfakes/memfakes.go` 对齐。
//! 不是生产实现;唯一目的是让 Runtime.step 端到端跑通。

#![allow(dead_code)]

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use agent_runtime_rs::context::{ContextEngine, ContextError};
use agent_runtime_rs::domain::{
    Context, Event, LLMResponse, Message, Session, SessionView, Task, TaskStatus, Tool, Turn, TurnStatus,
};
use agent_runtime_rs::event_payloads::{
    EventPayload, PayloadToolCalled, PayloadToolReturned,
};
use agent_runtime_rs::executor::{Executor, ExecutorError};
use agent_runtime_rs::llm::{LLMError, LLMProvider};
use agent_runtime_rs::prompt::{Messages, PromptCompiler, PromptError};
use agent_runtime_rs::runtime::Runtime;
use agent_runtime_rs::state::{EventStore, State, StateError};

// ---------- EventStore ----------

pub struct EventStoreFake {
    events: Vec<Event>,
    next_id: usize,
    seq_by: HashMap<String, i64>,
}

impl EventStoreFake {
    pub fn new() -> Self {
        Self { events: Vec::new(), next_id: 0, seq_by: HashMap::new() }
    }
    pub fn snapshot(&self) -> Vec<Event> {
        self.events.clone()
    }
}

impl EventStore for EventStoreFake {
    fn append(&mut self, events: &mut [Event]) -> Result<(), StateError> {
        for e in events.iter_mut() {
            if e.id.is_empty() {
                self.next_id += 1;
                e.id = format!("e{:02}", self.next_id);
            }
            let entry = self.seq_by.entry(e.session_id.clone()).or_insert(0);
            if e.seq == 0 {
                *entry += 1;
                e.seq = *entry;
            } else if e.seq > *entry {
                *entry = e.seq;
            }
            self.events.push(e.clone());
        }
        Ok(())
    }
    fn load(&self, session_id: &str) -> Result<Vec<Event>, StateError> {
        self.load_from(session_id, 0)
    }
    fn load_from(&self, session_id: &str, from_seq: i64) -> Result<Vec<Event>, StateError> {
        Ok(self
            .events
            .iter()
            .filter(|e| e.session_id == session_id && e.seq > from_seq)
            .cloned()
            .collect())
    }
}

// ---------- State ----------

pub struct StateFake {
    views: HashMap<String, SessionView>,
}

impl StateFake {
    pub fn new() -> Self { Self { views: HashMap::new() } }

    /// 把已折叠的 View 作为初始状态注入。§3.6.3 恢复流程用。
    pub fn load_snapshot(&mut self, session_id: &str, view: SessionView) {
        self.views.insert(session_id.into(), view);
    }
}

impl State for StateFake {
    fn apply(&mut self, events: &[Event]) -> Result<(), StateError> {
        for e in events {
            let view = self.views.entry(e.session_id.clone()).or_insert_with(SessionView::default);
            check_invariants(view, e)?;
            apply_one(view, e);
            if e.seq > view.max_seq {
                view.max_seq = e.seq;
            }
            if !e.id.is_empty() {
                view.seen_ids.insert(e.id.clone());
            }
        }
        Ok(())
    }
    fn view(&self, session_id: &str) -> Result<SessionView, StateError> {
        self.views
            .get(session_id)
            .cloned()
            .ok_or_else(|| StateError(format!("no view for session {}", session_id)))
    }
}

/// 与 Go 版 checkInvariants 对齐:seq 只在 >0 时做单调校验(ch01 手工样本 seq=0 跳过);
/// caused_by 只在 seen_ids 非空时校验。见 ch03 §3.5.4。
fn check_invariants(view: &SessionView, e: &Event) -> Result<(), StateError> {
    if e.session_id.is_empty() {
        return Err(StateError("event.session_id is empty".into()));
    }
    if e.seq > 0 && e.seq <= view.max_seq {
        return Err(StateError(format!(
            "event seq {} not strictly greater than view max_seq {} (id={})",
            e.seq, view.max_seq, e.id
        )));
    }
    if !e.caused_by.is_empty() && !view.seen_ids.is_empty() && !view.seen_ids.contains(&e.caused_by) {
        return Err(StateError(format!(
            "event {} references unknown caused_by={}",
            e.id, e.caused_by
        )));
    }
    Ok(())
}

fn apply_one(view: &mut SessionView, e: &Event) {
    match &e.payload {
        EventPayload::SessionOpened(p) => {
            view.session = Session {
                id: e.session_id.clone(),
                principal: p.principal.clone(),
                created_at: e.ts,
                last_active_at: e.ts,
                metadata: p.metadata.clone(),
            };
        }
        EventPayload::TaskCreated(p) => {
            view.tasks.insert(
                e.task_id.clone(),
                Task {
                    id: e.task_id.clone(),
                    session_id: e.session_id.clone(),
                    goal: p.goal.clone(),
                    status: TaskStatus::Running,
                    budget: p.budget,
                    started_at: e.ts,
                    ended_at: None,
                },
            );
        }
        EventPayload::TaskEnded(p) => {
            if let Some(t) = view.tasks.get_mut(&e.task_id) {
                t.status = p.status;
                t.ended_at = e.ts;
            }
        }
        EventPayload::TurnStarted(p) => {
            view.last_turn.insert(
                e.task_id.clone(),
                Turn {
                    id: e.turn_id.clone(),
                    task_id: e.task_id.clone(),
                    index: p.index,
                    status: TurnStatus::Running,
                    ..Default::default()
                },
            );
        }
        EventPayload::TurnEnded(p) => {
            let mut index = 0i32;
            if let Some(t) = view.last_turn.get_mut(&e.task_id) {
                t.status = p.status;
                t.tokens_in = p.tokens_in;
                t.tokens_out = p.tokens_out;
                t.cost_us = p.cost_us;
                t.latency_ms = p.latency_ms;
                index = t.index;
            }
            // ch04: 追加 TurnDigest 到 WorkingSet(§4.4.1)。
            view.working_set.push(agent_runtime_rs::summary::TurnDigest {
                turn_id: e.turn_id.clone(),
                task_id: e.task_id.clone(),
                index,
                from_seq: e.seq,
                to_seq: e.seq,
                superseded: false,
            });
        }
        EventPayload::ContextCompressed(p) => {
            // ch04 §4.5.3: 存 Summary + mark 覆盖的 TurnDigest 为 Superseded。
            view.summaries.insert(p.from_seq, p.summary.clone());
            for d in view.working_set.iter_mut() {
                if d.to_seq >= p.from_seq && d.from_seq <= p.to_seq {
                    d.superseded = true;
                }
            }
        }
        EventPayload::ProgressUpdated(p) => {
            view.progresses.insert(p.task_id.clone(), p.progress.clone());
        }
        EventPayload::MemoryQueried(p) => {
            view.memory_refs.extend(p.refs.iter().cloned());
        }
        _ => {}
    }
    if e.ts.is_some() {
        view.session.last_active_at = e.ts;
    }
}

// ---------- ContextEngine ----------

pub struct ContextEngineFake {
    store: Arc<Mutex<EventStoreFake>>,
    tools: Vec<Tool>,
}

impl ContextEngineFake {
    pub fn new(store: Arc<Mutex<EventStoreFake>>, tools: Vec<Tool>) -> Self {
        Self { store, tools }
    }
}

impl ContextEngine for ContextEngineFake {
    fn assemble(&self, session_id: &str, task_id: &str) -> Result<Context, ContextError> {
        let events = self.store.lock().unwrap().snapshot();
        let mut msgs = vec![Message {
            role: "system".into(),
            content: "you are an agent.".into(),
            ..Default::default()
        }];
        for e in &events {
            if !e.task_id.is_empty() && e.task_id != task_id {
                continue;
            }
            match &e.payload {
                EventPayload::UserSpoke(p) => msgs.push(Message {
                    role: "user".into(),
                    content: p.text.clone(),
                    ..Default::default()
                }),
                EventPayload::LLMReplied(p) => {
                    let mut m = p.assistant.clone();
                    if m.role.is_empty() {
                        m.role = "assistant".into();
                    }
                    m.tool_calls = p.tool_calls.clone();
                    msgs.push(m);
                }
                EventPayload::ToolReturned(p) => msgs.push(Message {
                    role: "tool".into(),
                    tool_call_id: p.call_id.clone(),
                    content: p.content.clone(),
                    ..Default::default()
                }),
                _ => {}
            }
        }
        Ok(Context {
            session_id: session_id.into(),
            task_id: task_id.into(),
            messages: msgs,
            tools: self.tools.clone(),
            ..Default::default()
        })
    }
}

// ---------- PromptCompiler ----------

pub struct PromptCompilerPassthrough;

impl PromptCompiler for PromptCompilerPassthrough {
    fn compile(&self, ctx: &Context) -> Result<Messages, PromptError> {
        Ok(ctx.messages.clone())
    }
}

// ---------- LLMProvider ----------

pub struct LLMScript {
    script: Vec<LLMResponse>,
    idx: Mutex<usize>,
}

impl LLMScript {
    pub fn new(script: Vec<LLMResponse>) -> Self {
        Self { script, idx: Mutex::new(0) }
    }
}

impl LLMProvider for LLMScript {
    fn chat(&self, _msgs: &Messages, _tools: &[Tool]) -> Result<LLMResponse, LLMError> {
        let mut i = self.idx.lock().unwrap();
        if *i >= self.script.len() {
            return Err(LLMError("llm script exhausted".into()));
        }
        let resp = self.script[*i].clone();
        *i += 1;
        Ok(resp)
    }
}

// ---------- Executor ----------

pub type ToolFn = Box<dyn Fn(&str) -> Result<String, String> + Send + Sync>;

pub struct ExecutorFake {
    store: Arc<Mutex<EventStoreFake>>,
    tools: Mutex<HashMap<String, ToolFn>>,
}

impl ExecutorFake {
    pub fn new(store: Arc<Mutex<EventStoreFake>>, tools: HashMap<String, ToolFn>) -> Self {
        Self { store, tools: Mutex::new(tools) }
    }
}

impl Executor for ExecutorFake {
    fn run(&self, turn: &Turn) -> Result<Vec<Event>, ExecutorError> {
        let all = self.store.lock().unwrap().snapshot();
        let last_replied = all
            .iter()
            .rev()
            .find(|e| e.turn_id == turn.id && matches!(e.payload, EventPayload::LLMReplied(_)));
        let calls = match last_replied {
            Some(e) => match &e.payload {
                EventPayload::LLMReplied(p) => p.tool_calls.clone(),
                _ => unreachable!(),
            },
            None => return Err(ExecutorError("no LLMReplied in current turn".into())),
        };
        let tools = self.tools.lock().unwrap();
        let mut out = Vec::with_capacity(calls.len() * 2);
        for call in &calls {
            out.push(Event {
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
            });
            let payload = match tools.get(&call.name) {
                Some(fn_) => match fn_(&call.arguments) {
                    Ok(content) => PayloadToolReturned {
                        call_id: call.id.clone(), content, is_error: false,
                    },
                    Err(err) => PayloadToolReturned {
                        call_id: call.id.clone(), content: err, is_error: true,
                    },
                },
                None => PayloadToolReturned {
                    call_id: call.id.clone(),
                    content: format!("unknown tool: {}", call.name),
                    is_error: true,
                },
            };
            out.push(Event {
                id: String::new(),
                session_id: String::new(),
                task_id: String::new(),
                turn_id: String::new(),
                ts: None,
                caused_by: String::new(),
                payload: EventPayload::ToolReturned(payload),
                seq: 0,
            });
        }
        Ok(out)
    }
}

// ---------- helpers 供 main/tests 追加系统级 Event ----------

pub fn append_all(rt: &Runtime, sid: &str, tid: &str, turn_id: &str, payloads: Vec<EventPayload>) {
    for p in payloads {
        let ev = Event {
            id: String::new(),
            session_id: sid.into(),
            task_id: tid.into(),
            turn_id: turn_id.into(),
            ts: None,
            caused_by: String::new(),
            payload: p,
            seq: 0,
        };
        let mut buf = [ev];
        rt.event_store.lock().unwrap().append(&mut buf).unwrap();
        rt.state.lock().unwrap().apply(&buf).unwrap();
    }
}
