//! ch02 · 端到端测试:验证 Runtime.step 在最小依赖下产出与 ch01 手工样本一致的关键指标。
//!
//! 复用 examples/ch02/fakes.rs 里的内存 fake(通过 #[path] 引入),避免重复。
//! 与 `runtime-go/examples/ch02/` 端到端 demo 逐字段对齐。

#[path = "../examples/ch02/fakes.rs"]
mod fakes;

use std::sync::{Arc, Mutex};

use agent_runtime_rs::domain::{
    Budget, EventPayload, LLMResponse, Message, PayloadSessionOpened, PayloadTaskCreated,
    PayloadTaskEnded, PayloadTurnStarted, PayloadUserSpoke, TaskStatus, Tool, ToolCall,
    TurnStatus,
};
use agent_runtime_rs::runtime::Runtime;
use agent_runtime_rs::state::State as _;

use fakes::{
    append_all, ContextEngineFake, EventStoreFake, ExecutorFake, LLMScript,
    PromptCompilerPassthrough, StateFake, ToolFn,
};

#[test]
fn ch02_end_to_end_matches_ch01_totals() {
    let store = Arc::new(Mutex::new(EventStoreFake::new()));
    let state = Arc::new(Mutex::new(StateFake::new()));

    let mut tools: std::collections::HashMap<String, ToolFn> = std::collections::HashMap::new();
    tools.insert("weather".into(), Box::new(|_| Ok(r#"{"temp":26,"sky":"多云"}"#.into())));
    tools.insert("send_email".into(), Box::new(|_| Ok(r#"{"ok":true}"#.into())));

    let tool_descs = vec![
        Tool { name: "weather".into(), ..Default::default() },
        Tool { name: "send_email".into(), ..Default::default() },
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
        context: Arc::new(ContextEngineFake::new(state.clone(), store.clone(), tool_descs)),
        prompt: Arc::new(PromptCompilerPassthrough),
        llm: Arc::new(LLMScript::new(script)),
        executor: Arc::new(ExecutorFake::new(store.clone(), tools)),
    };

    let sid = "s1";
    let tid = "t1";

    append_all(&rt, sid, "", "", vec![
        EventPayload::SessionOpened(PayloadSessionOpened {
            principal: "user-42".into(), ..Default::default()
        }),
        EventPayload::UserSpoke(PayloadUserSpoke {
            text: "查天气 + 发邮件".into(),
        }),
    ]);
    append_all(&rt, sid, tid, "", vec![
        EventPayload::TaskCreated(PayloadTaskCreated {
            goal: "查天气 + 发邮件".into(),
            budget: Budget { max_tokens: 8000, ..Default::default() },
        }),
    ]);

    for (i, turn_id) in ["r1", "r2", "r3"].iter().enumerate() {
        append_all(&rt, sid, tid, turn_id, vec![
            EventPayload::TurnStarted(PayloadTurnStarted { index: i as i32 }),
        ]);
        rt.step(sid, tid, turn_id).expect("step failed");
    }
    append_all(&rt, sid, tid, "", vec![
        EventPayload::TaskEnded(PayloadTaskEnded {
            status: TaskStatus::Succeeded, reason: String::new(),
        }),
    ]);

    let events = store.lock().unwrap().snapshot();

    // Runtime 版本比 ch01 手工样本多一条:e17(LLMRequested)在 ch01 里被省略了。
    assert_eq!(events.len(), 20, "expect 20 events (19 in ch01 + explicit LLMRequested in Turn 3)");

    let view = state.lock().unwrap().view(sid).unwrap();
    let task = view.tasks.get(tid).expect("task missing");
    assert_eq!(task.status, TaskStatus::Succeeded);

    let last = view.last_turn.get(tid).expect("last turn missing");
    assert_eq!(last.id, "r3");
    assert_eq!(last.index, 2);
    assert_eq!(last.status, TurnStatus::Done);

    // tokens_in 汇总:520 + 610 + 700 = 1830,与 ch01 样本一致
    let total_in: i64 = events
        .iter()
        .filter_map(|e| match &e.payload {
            EventPayload::TurnEnded(p) => Some(p.tokens_in),
            _ => None,
        })
        .sum();
    assert_eq!(total_in, 1830);
}
