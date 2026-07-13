//! Runtime 协调器 —— 把 6 个接口串起来。
//! 对应章节:ch02-runtime-dataflow.md §2.4。与 `runtime-go/runtime/runtime.go` 对齐。

use std::sync::{Arc, Mutex};

use crate::context::ContextEngine;
use crate::domain::{
    Event, EventPayload, PayloadLLMReplied, PayloadLLMRequested, PayloadTurnEnded, Turn,
};
use crate::executor::Executor;
use crate::llm::LLMProvider;
use crate::prompt::PromptCompiler;
use crate::state::{EventStore, State};

/// Runtime 持有 6 个协作接口。
///
/// 依赖用 `Arc<Mutex<..>>` 封装是为了让 example / 集成测试里可以从外部继续追加事件;
/// 生产版本会有更精细的并发策略,见 ch04/ch08。
pub struct Runtime {
    pub event_store: Arc<Mutex<dyn EventStore + Send>>,
    pub state: Arc<Mutex<dyn State + Send>>,
    pub context: Arc<dyn ContextEngine + Send + Sync>,
    pub prompt: Arc<dyn PromptCompiler + Send + Sync>,
    pub llm: Arc<dyn LLMProvider + Send + Sync>,
    pub executor: Arc<dyn Executor + Send + Sync>,
}

#[derive(Debug)]
pub struct StepError(pub String);

impl Runtime {
    /// 驱动一个 Turn 完成,产出这一 Turn 追加的 Event 数组。
    ///
    /// 与 Go 版一致:Fold → Project → Compile → Chat → Emit。
    pub fn step(
        &self,
        session_id: &str,
        task_id: &str,
        turn_id: &str,
    ) -> Result<Vec<Event>, StepError> {
        // 前置:TurnStarted 应由调用方追加。
        {
            let st = self.state.lock().unwrap();
            let view = st.view(session_id).map_err(|e| StepError(e.0))?;
            match view.last_turn.get(task_id) {
                Some(t) if t.id == turn_id && t.status == crate::domain::TurnStatus::Running => {}
                Some(t) if t.id == turn_id => {
                    return Err(StepError("turn already completed".into()));
                }
                _ => {
                    return Err(StepError(
                        "turn not started (append TurnStarted before step)".into(),
                    ))
                }
            }
        }

        let mut last_appended_id = String::new();
        if let Ok(prior) = {
            let store = self.event_store.lock().unwrap();
            store.load(session_id)
        } {
            if let Some(last) = prior.last() {
                last_appended_id = last.id.clone();
            }
        }

        let mut appended = Vec::new();

        // ---- Fold + Project ----
        let mut ctx = self
            .context
            .assemble(session_id, task_id)
            .map_err(|e| StepError(e.0))?;
        ctx.turn_id = turn_id.into();

        // ---- Compile ----
        let msgs = self.prompt.compile(&ctx).map_err(|e| StepError(e.0))?;

        // ---- Chat ----
        self.append_one(
            session_id,
            task_id,
            turn_id,
            EventPayload::LLMRequested(PayloadLLMRequested {
                model: "reference".into(),
                messages: msgs.clone(),
                tools: ctx.tools.clone(),
            }),
            &mut appended,
            &mut last_appended_id,
        )?;
        let resp = self
            .llm
            .chat(&msgs, &ctx.tools)
            .map_err(|e| StepError(e.0))?;
        let tokens_in = resp.tokens_in;
        let tokens_out = resp.tokens_out;
        let tool_calls = resp.tool_calls.clone();
        self.append_one(
            session_id,
            task_id,
            turn_id,
            EventPayload::LLMReplied(PayloadLLMReplied {
                assistant: resp.assistant,
                tool_calls: resp.tool_calls,
                tokens_in,
                tokens_out,
            }),
            &mut appended,
            &mut last_appended_id,
        )?;

        // ---- Emit: Executor 处理工具调用 ----
        if !tool_calls.is_empty() {
            let tool_turn = Turn {
                id: turn_id.into(),
                session_id: session_id.into(),
                task_id: task_id.into(),
                ..Default::default()
            };
            let tool_events = self.executor.run(&tool_turn).map_err(|e| StepError(e.0))?;
            for mut e in tool_events {
                e.session_id = session_id.into();
                e.task_id = task_id.into();
                e.turn_id = turn_id.into();
                self.append_raw(e, &mut appended, &mut last_appended_id)?;
            }
        }

        // ---- TurnEnded ----
        self.append_one(
            session_id,
            task_id,
            turn_id,
            EventPayload::TurnEnded(PayloadTurnEnded {
                status: crate::domain::TurnStatus::Done,
                tokens_in,
                tokens_out,
                ..Default::default()
            }),
            &mut appended,
            &mut last_appended_id,
        )?;
        Ok(appended)
    }

    fn append_one(
        &self,
        sid: &str,
        tid: &str,
        turn: &str,
        payload: EventPayload,
        appended: &mut Vec<Event>,
        last_appended_id: &mut String,
    ) -> Result<(), StepError> {
        let e = Event {
            id: String::new(),
            session_id: sid.into(),
            task_id: tid.into(),
            turn_id: turn.into(),
            ts: None,
            caused_by: String::new(),
            payload,
            seq: 0,
        };
        self.append_raw(e, appended, last_appended_id)
    }

    fn append_raw(
        &self,
        mut e: Event,
        appended: &mut Vec<Event>,
        last_appended_id: &mut String,
    ) -> Result<(), StepError> {
        if e.caused_by.is_empty() && !last_appended_id.is_empty() {
            e.caused_by = last_appended_id.clone();
        }
        // 用一个可变 buffer 让 EventStore 把 seq / id / ts 写回到 buf[0]。
        let mut buf = [e];
        {
            let mut store = self.event_store.lock().unwrap();
            store.append(&mut buf).map_err(|x| StepError(x.0))?;
        }
        {
            let mut st = self.state.lock().unwrap();
            st.apply(&buf).map_err(|x| StepError(x.0))?;
        }
        let [ev] = buf;
        *last_appended_id = ev.id.clone();
        appended.push(ev);
        Ok(())
    }
}
