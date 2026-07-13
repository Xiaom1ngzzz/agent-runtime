//! LayeredContextEngine —— 六层输入的确定性只读投影。见 ch04 §4.4。
//!
//! 与 `runtime-go/context/layered.go` 对齐。
//!
//! **必须是确定性的只读投影**:不写状态、不读时钟、不发起 LLM/Memory 请求。
//! 摘要/检索由 Compressor(ch04 §4.5)在上游产出 Event;Assemble 只读 State,
//! 并可从 EventStore 展开 WorkingSet 指向的消息原文。

use std::collections::HashSet;
use std::sync::{Arc, Mutex};

use super::{ContextEngine, ContextError};
use crate::domain::{
    Context, Event, EventPayload, MemoryRef, Message, Progress, Summary, Task, Tool, TurnDigest,
};
use crate::state::{EventStore, State};

/// ch04 §4.4 的落地实现。
///
/// Assemble 不调 LLM、不写外部状态;唯一 IO 是确定性地只读 State/EventStore。
pub struct LayeredContextEngine {
    pub state: Arc<Mutex<dyn State + Send>>,
    pub store: Option<Arc<Mutex<dyn EventStore + Send>>>,
    pub instructions: String,
    pub tools: Vec<Tool>,
}

impl ContextEngine for LayeredContextEngine {
    fn assemble(&self, session_id: &str, task_id: &str) -> Result<Context, ContextError> {
        let view = {
            let st = self.state.lock().unwrap();
            st.view(session_id).map_err(|e| ContextError(e.0))?
        };

        let mut msgs: Vec<Message> = Vec::new();

        // 1. Instructions
        if !self.instructions.is_empty() {
            msgs.push(Message {
                role: "system".into(),
                content: self.instructions.clone(),
                ..Default::default()
            });
        }

        // 2. Task Frame
        if let Some(task) = view.tasks.get(task_id) {
            msgs.push(Message {
                role: "system".into(),
                content: render_task_frame(task),
                ..Default::default()
            });
        }

        // 3. Progress
        if let Some(progress) = view.progresses.get(task_id) {
            msgs.push(Message {
                role: "system".into(),
                content: render_progress(progress),
                ..Default::default()
            });
        }

        // 4. Compressed History —— 覆盖 seq 落在 WorkingSet 之外的 Summary。
        let min_seq = working_set_min_seq(&view.working_set);
        let mut sum_keys: Vec<i64> = view.summaries.keys().copied().collect();
        sum_keys.sort_unstable();
        for from_seq in sum_keys {
            let sum = &view.summaries[&from_seq];
            if !sum.task_id.is_empty() && !task_id.is_empty() && sum.task_id != task_id {
                continue;
            }
            if min_seq > 0 && sum.to_seq >= min_seq {
                continue;
            }
            msgs.push(Message {
                role: "system".into(),
                content: render_summary(sum),
                ..Default::default()
            });
        }

        // 5. Working Set 展开原文(只读 seq <= view.max_seq 的事件)。
        if let Some(store_arc) = &self.store {
            let events: Vec<Event> = {
                let s = store_arc.lock().unwrap();
                s.load(session_id).map_err(|e| ContextError(e.0))?
            };
            let active = active_turn_set(&view.working_set, task_id);
            for ev in &events {
                if view.max_seq > 0 && ev.seq > view.max_seq {
                    continue;
                }
                if !active.contains(&ev.turn_id) {
                    continue;
                }
                append_turn_message(&mut msgs, ev);
            }
        }

        // 6. Memory Refs:放在 Working Set 之后,使检索证据靠近消息尾部。
        for r in &view.memory_refs {
            msgs.push(Message {
                role: "system".into(),
                content: render_memory_ref(r),
                ..Default::default()
            });
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

// ---------- helpers (全是纯函数) ----------

fn render_task_frame(task: &Task) -> String {
    format!(
        "<task_frame>\ngoal: {}\nbudget_tokens: {}\n</task_frame>",
        task.goal, task.budget.max_tokens
    )
}

fn render_progress(p: &Progress) -> String {
    let mut out = format!(
        "<task_progress version={} updated_at=\"{}\">\ngoal: {}\n",
        p.version, p.updated_at, p.goal
    );
    for step in &p.done {
        out.push_str(&format!(
            "done: [{:?}] {} | {} | {}\n",
            step.kind, step.intent, step.action, step.observation
        ));
    }
    for step in &p.next {
        out.push_str(&format!(
            "next: [{:?}] {} | {}\n",
            step.kind, step.intent, step.action
        ));
    }
    for open in &p.open {
        out.push_str(&format!(
            "open: {} (raised_at={})\n",
            open.question, open.raised_at
        ));
    }
    out.push_str("</task_progress>");
    out
}

fn render_summary(s: &Summary) -> String {
    let mut out = String::from("<prior_summary>\n");
    if !s.user_intents.is_empty() {
        out.push_str(&format!("user_intents: {:?}\n", s.user_intents));
    }
    if !s.tool_results.is_empty() {
        out.push_str(&format!("tool_results: {:?}\n", s.tool_results));
    }
    for d in &s.decisions_made {
        out.push_str(&format!(
            "decision(@seq={}): {} — {}\n",
            d.at_seq, d.what, d.why
        ));
    }
    if !s.open_questions.is_empty() {
        out.push_str(&format!("open_questions: {:?}\n", s.open_questions));
    }
    if !s.next_actions.is_empty() {
        out.push_str(&format!("next_actions: {:?}\n", s.next_actions));
    }
    out.push_str("</prior_summary>");
    out
}

fn render_memory_ref(r: &MemoryRef) -> String {
    let content = xml_escape(&r.content);
    format!(
        "<memory_ref source=\"{}\" score={:.2}>\n{}\n</memory_ref>",
        xml_escape(&r.source),
        r.score,
        content
    )
}

fn xml_escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
}

fn working_set_min_seq(ws: &[TurnDigest]) -> i64 {
    let mut min = 0i64;
    for d in ws {
        if d.superseded {
            continue;
        }
        if min == 0 || d.from_seq < min {
            min = d.from_seq;
        }
    }
    min
}

fn active_turn_set(ws: &[TurnDigest], task_id: &str) -> HashSet<String> {
    let mut out = HashSet::new();
    for d in ws {
        if d.superseded {
            continue;
        }
        if !d.task_id.is_empty() && !task_id.is_empty() && d.task_id != task_id {
            continue;
        }
        out.insert(d.turn_id.clone());
    }
    out
}

fn append_turn_message(msgs: &mut Vec<Message>, e: &Event) {
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
