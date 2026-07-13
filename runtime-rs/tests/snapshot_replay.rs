//! ch03 §3.7.4 承诺的端到端证据 —— Rust 版。与 `runtime-go/state/snapshot_test.go` 对齐。
//!
//! 用 ch02 那份 20 条 Event 的场景喂给 Runtime,每追加一条 TurnEnded 就拍一个 Snapshot,
//! 丢弃当前 State,从最新 Snapshot + load_from 恢复,断言恢复出的 View 与"从零 Fold"相等。

#[path = "../examples/ch02/fakes.rs"]
mod fakes;

use std::sync::{Arc, Mutex};

use agent_runtime_rs::domain::{
    Budget, Event, EventPayload, LLMResponse, Message, PayloadSessionOpened, PayloadTaskCreated,
    PayloadTaskEnded, PayloadTurnStarted, PayloadUserSpoke, SessionView, TaskStatus, Tool,
    ToolCall, TurnStatus,
};
use agent_runtime_rs::runtime::Runtime;
use agent_runtime_rs::state::{
    EventStore as _, MemSnapshotStore, Snapshot, SnapshotStore, State as _,
};

use fakes::{
    append_all, ContextEngineFake, EventStoreFake, ExecutorFake, LLMScript, PromptCompilerPassthrough,
    StateFake, ToolFn,
};

#[test]
fn snapshot_replay() {
    // ---- 构造与 ch02 相同的 Runtime ----
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
        EventPayload::SessionOpened(PayloadSessionOpened { principal: "user-42".into(), ..Default::default() }),
        EventPayload::UserSpoke(PayloadUserSpoke { text: "查天气 + 发邮件".into() }),
    ]);
    append_all(&rt, sid, tid, "", vec![
        EventPayload::TaskCreated(PayloadTaskCreated {
            goal: "查天气 + 发邮件".into(),
            budget: Budget { max_tokens: 8000, ..Default::default() },
            parent_id: String::new(),
        }),
    ]);

    // ---- 每个 Turn 结束时拍快照 ----
    let mut snap_store = MemSnapshotStore::new();
    for (i, turn_id) in ["r1", "r2", "r3"].iter().enumerate() {
        append_all(&rt, sid, tid, turn_id, vec![
            EventPayload::TurnStarted(PayloadTurnStarted { index: i as i32 }),
        ]);
        rt.step(sid, tid, turn_id).expect("step");
        let view = state.lock().unwrap().view(sid).unwrap();
        snap_store
            .save(sid, Snapshot { seq: view.max_seq, view })
            .expect("save snapshot");
    }
    append_all(&rt, sid, tid, "", vec![
        EventPayload::TaskEnded(PayloadTaskEnded { status: TaskStatus::Succeeded, reason: String::new() }),
    ]);

    // ---- "重启":新 State,从 Snapshot + load_from 恢复 ----
    let snap = snap_store.latest(sid).unwrap().expect("no snapshot");

    let mut fresh = StateFake::new();
    fresh.load_snapshot(sid, snap.view.clone());

    // 最后一次 snapshot 停在 e19=TurnEnded/r3,它之后只追加了 e20=TaskEnded。
    let remaining = store.lock().unwrap().load_from(sid, snap.seq).unwrap();
    assert_eq!(
        remaining.len(),
        1,
        "expected exactly 1 event replayed after latest snapshot (e20=TaskEnded)"
    );
    fresh.apply(&remaining).unwrap();

    // ---- 断言:恢复出的 View 与"从零 Fold 全部事件"相等 ----
    let recovered = fresh.view(sid).unwrap();

    let all_events: Vec<Event> = store.lock().unwrap().snapshot();
    let mut full_state = StateFake::new();
    full_state.apply(&all_events).unwrap();
    let full = full_state.view(sid).unwrap();

    assert!(
        views_equal(&recovered, &full),
        "recovered != full:\n  recovered={:?}\n  full={:?}",
        recovered,
        full,
    );
}

#[test]
fn snapshot_replay_seq_regression_rejected() {
    // §3.5.4:seq 逆序应被 State.Apply 拒绝。
    let mut st = StateFake::new();
    let sid = "s1";

    let mut e1 = Event {
        id: "e01".into(),
        session_id: sid.into(),
        task_id: String::new(),
        turn_id: String::new(),
        ts: None,
        caused_by: String::new(),
        payload: EventPayload::SessionOpened(PayloadSessionOpened {
            principal: "u".into(),
            ..Default::default()
        }),
        seq: 1,
    };
    let mut e2 = Event {
        id: "e02".into(),
        session_id: sid.into(),
        task_id: String::new(),
        turn_id: String::new(),
        ts: None,
        caused_by: String::new(),
        payload: EventPayload::UserSpoke(PayloadUserSpoke { text: "hi".into() }),
        seq: 2,
    };
    // 借助 slice apply
    st.apply(std::slice::from_ref(&mut e1 as &Event)).unwrap();
    st.apply(std::slice::from_ref(&mut e2 as &Event)).unwrap();

    let regression = Event {
        id: "e02b".into(),
        session_id: sid.into(),
        task_id: String::new(),
        turn_id: String::new(),
        ts: None,
        caused_by: String::new(),
        payload: EventPayload::UserSpoke(PayloadUserSpoke { text: "regression".into() }),
        seq: 2, // 与已有 max_seq 相同,应拒绝
    };
    let err = st.apply(std::slice::from_ref(&regression));
    assert!(err.is_err(), "expected seq regression to be rejected");
}

fn views_equal(a: &SessionView, b: &SessionView) -> bool {
    if a.session.id != b.session.id || a.session.principal != b.session.principal {
        return false;
    }
    if a.tasks.len() != b.tasks.len() || a.last_turn.len() != b.last_turn.len() {
        return false;
    }
    for (k, va) in &a.tasks {
        match b.tasks.get(k) {
            Some(vb) if va.status == vb.status && va.goal == vb.goal => {}
            _ => return false,
        }
    }
    for (k, va) in &a.last_turn {
        match b.last_turn.get(k) {
            Some(vb)
                if va.id == vb.id
                    && va.index == vb.index
                    && va.status == vb.status
                    && va.tokens_in == vb.tokens_in
                    && va.tokens_out == vb.tokens_out => {}
            _ => return false,
        }
    }
    // TurnStatus 需要 PartialEq —— 已经在 domain.rs 上派生。
    let _ = TurnStatus::default();
    true
}
