//! ch02 · 端到端跑一次"查天气 + 发邮件"。这次通过 Runtime.step 生成事件,
//! 而不是像 ch01 那样手写。产出对齐 ch01 样本(19 → 20 条 Event、3 个 Turn、tokens_in=1830)。
//!
//! 与 `runtime-go/examples/ch02/main.go` 逐一对齐。
//!
//! ```bash
//! cargo run --example ch02
//! ```

mod fakes;

use std::sync::{Arc, Mutex};

use agent_runtime_rs::domain::{
    Budget, LLMResponse, Message, TaskStatus, Tool, ToolCall,
};
use agent_runtime_rs::event_payloads::{
    EventPayload, PayloadSessionOpened, PayloadTaskCreated, PayloadTaskEnded,
    PayloadTurnStarted, PayloadUserSpoke,
};
use agent_runtime_rs::runtime::Runtime;
use agent_runtime_rs::state::State as _;

use fakes::{ContextEngineFake, EventStoreFake, ExecutorFake, LLMScript, PromptCompilerPassthrough, StateFake, ToolFn};

fn main() {
    let store = Arc::new(Mutex::new(EventStoreFake::new()));
    let state = Arc::new(Mutex::new(StateFake::new()));

    let mut tools: std::collections::HashMap<String, ToolFn> = std::collections::HashMap::new();
    tools.insert(
        "weather".into(),
        Box::new(|_| Ok(r#"{"temp":26,"sky":"多云"}"#.into())),
    );
    tools.insert(
        "send_email".into(),
        Box::new(|_| Ok(r#"{"ok":true}"#.into())),
    );

    let tool_descs = vec![
        Tool { name: "weather".into(), description: "查天气".into(), ..Default::default() },
        Tool { name: "send_email".into(), description: "发邮件".into(), ..Default::default() },
    ];

    let script = vec![
        LLMResponse {
            assistant: Message { role: "assistant".into(), ..Default::default() },
            tool_calls: vec![ToolCall {
                id: "c1".into(), name: "weather".into(),
                arguments: r#"{"city":"北京","date":"2026-07-10"}"#.into(),
            }],
            tokens_in: 520, tokens_out: 48,
        },
        LLMResponse {
            assistant: Message { role: "assistant".into(), ..Default::default() },
            tool_calls: vec![ToolCall {
                id: "c2".into(), name: "send_email".into(),
                arguments: r#"{"to":"alice@example.com","body":"..."}"#.into(),
            }],
            tokens_in: 610, tokens_out: 72,
        },
        LLMResponse {
            assistant: Message {
                role: "assistant".into(),
                content: "已经发送提醒邮件给 Alice。".into(),
                ..Default::default()
            },
            tokens_in: 700, tokens_out: 20,
            ..Default::default()
        },
    ];

    let rt = Runtime {
        event_store: store.clone(),
        state: state.clone(),
        context: Arc::new(ContextEngineFake::new(store.clone(), tool_descs)),
        prompt: Arc::new(PromptCompilerPassthrough),
        llm: Arc::new(LLMScript::new(script)),
        executor: Arc::new(ExecutorFake::new(store.clone(), tools)),
    };

    let sid = "s1";
    let tid = "t1";

    fakes::append_all(&rt, sid, "", "", vec![
        EventPayload::SessionOpened(PayloadSessionOpened {
            principal: "user-42".into(), ..Default::default()
        }),
        EventPayload::UserSpoke(PayloadUserSpoke {
            text: "帮我查一下明天北京的天气,然后写一封提醒邮件给 alice@example.com".into(),
        }),
    ]);
    fakes::append_all(&rt, sid, tid, "", vec![
        EventPayload::TaskCreated(PayloadTaskCreated {
            goal: "查天气 + 发邮件".into(),
            budget: Budget { max_tokens: 8000, ..Default::default() },
        }),
    ]);

    for (i, turn_id) in ["r1", "r2", "r3"].iter().enumerate() {
        fakes::append_all(&rt, sid, tid, turn_id, vec![
            EventPayload::TurnStarted(PayloadTurnStarted { index: i as i32 }),
        ]);
        rt.step(sid, tid, turn_id).expect("step failed");
    }
    fakes::append_all(&rt, sid, tid, "", vec![
        EventPayload::TaskEnded(PayloadTaskEnded {
            status: TaskStatus::Succeeded, reason: String::new(),
        }),
    ]);

    // ---- 汇总 ----
    let events = store.lock().unwrap().snapshot();
    println!("== Event 流({} 条) ==", events.len());
    for e in &events {
        println!(
            "  {:<3} {:<20} session={} task={:<3} turn={:<3}",
            e.id,
            e.payload.event_type(),
            e.session_id,
            if e.task_id.is_empty() { "-" } else { e.task_id.as_str() },
            if e.turn_id.is_empty() { "-" } else { e.turn_id.as_str() },
        );
    }
    println!();

    let view = state.lock().unwrap().view(sid).unwrap();
    println!("== 折叠后的 SessionView ==");
    println!("  session:  id={} principal={}", view.session.id, view.session.principal);
    for (tid, task) in &view.tasks {
        println!("  task:     id={} goal={:?} status={:?}", tid, task.goal, task.status);
    }
    for (tid, turn) in &view.last_turn {
        println!(
            "  turn:     task={} id={} index={} status={:?} tokens_in={} tokens_out={}",
            tid, turn.id, turn.index, turn.status, turn.tokens_in, turn.tokens_out
        );
    }
    let total_in: i64 = events
        .iter()
        .filter_map(|e| match &e.payload {
            EventPayload::TurnEnded(p) => Some(p.tokens_in),
            _ => None,
        })
        .sum();
    println!("  total tokens_in: {}", total_in);
}
