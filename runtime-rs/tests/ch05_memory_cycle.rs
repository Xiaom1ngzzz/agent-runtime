//! ch05 §5.10.2 端到端证据 —— Rust 版。与 `runtime-go/memory/ch05_memory_cycle_test.go` 对齐。

#[path = "../examples/ch02/fakes.rs"]
mod fakes;

use std::sync::{Arc, Mutex};

use agent_runtime_rs::context::{ContextEngine, LayeredContextEngine};
use agent_runtime_rs::domain::{
    Event, EventPayload, PayloadMemoryQueried, PayloadSessionOpened, PayloadTaskCreated,
    PayloadTurnStarted, PayloadUserSpoke,
};
use agent_runtime_rs::memory::{
    embed_text, InMemStore, MemoryItem, MemoryKind, MemoryStore, Query,
};
use agent_runtime_rs::state::{EventStore as _, State as _};

use fakes::{EventStoreFake, StateFake};

const EMBED_DIM: usize = 64;

#[test]
fn ch05_memory_cycle() {
    let mem = InMemStore::new();

    // 1. 导入 4 条种子
    mem.upsert(MemoryItem {
        id: "m1".into(),
        source: "user_pref".into(),
        kind: MemoryKind::Semantic,
        key: "user:42:diet".into(),
        content: "不吃辣,偏好清淡".into(),
        embedding: embed_text("不吃辣", EMBED_DIM),
        tags: vec!["user:42".into()],
        version: 1,
        ..Default::default()
    })
    .unwrap();
    mem.upsert(MemoryItem {
        id: "m2".into(),
        source: "user_pref".into(),
        kind: MemoryKind::Semantic,
        key: "user:42:email".into(),
        content: "xiaoming@example.com".into(),
        embedding: embed_text("邮箱 email", EMBED_DIM),
        tags: vec!["user:42".into()],
        version: 1,
        ..Default::default()
    })
    .unwrap();
    mem.upsert(MemoryItem {
        id: "m3".into(),
        source: "kb.docs".into(),
        kind: MemoryKind::Semantic,
        key: "kb:travel:policy".into(),
        content: "差旅政策".into(),
        embedding: embed_text("差旅政策 travel policy", EMBED_DIM),
        tags: vec!["domain:travel".into()],
        version: 1,
        ..Default::default()
    })
    .unwrap();
    mem.upsert(MemoryItem {
        id: "m4".into(),
        source: "session_summary".into(),
        kind: MemoryKind::Episodic,
        key: "session:s0:t0".into(),
        content: "上次订了周三从北京到上海的机票".into(),
        embedding: embed_text("订机票 北京 上海", EMBED_DIM),
        tags: vec!["user:42".into(), "domain:travel".into()],
        origin_session: "s0".into(),
        origin_task_id: "t0".into(),
        origin_seq_from: 1,
        origin_seq_to: 40,
        version: 1,
        ..Default::default()
    })
    .unwrap();

    // 断言 A: 幂等 upsert
    let same = MemoryItem {
        id: "m1".into(),
        source: "user_pref".into(),
        kind: MemoryKind::Semantic,
        key: "user:42:diet".into(),
        content: "不吃辣,偏好清淡".into(),
        embedding: embed_text("不吃辣", EMBED_DIM),
        tags: vec!["user:42".into()],
        version: 1,
        ..Default::default()
    };
    mem.upsert(same).unwrap();

    let refs = mem
        .query(&Query {
            keywords: vec!["user:42:diet".into()],
            top_k: 10,
            min_score: 0.0,
            ..Default::default()
        })
        .unwrap();
    let diet_count = refs.iter().filter(|r| r.key == "user:42:diet").count();
    assert_eq!(diet_count, 1, "idempotent upsert should not duplicate");

    // 2. 组合查询
    let refs = mem
        .query(&Query {
            semantic: "帮 alice 订机票".into(),
            tags: vec!["user:42".into()],
            source_filter: vec!["session_summary".into(), "user_pref".into()],
            top_k: 3,
            min_score: 0.0,
            ..Default::default()
        })
        .unwrap();
    assert!(!refs.is_empty(), "expected refs from combined query");

    // 断言 B: score 降序
    for i in 1..refs.len() {
        assert!(
            refs[i].score <= refs[i - 1].score,
            "refs not sorted by score desc"
        );
    }

    // 3. 追加 MemoryQueried Event 到 EventStore + Apply
    let store = Arc::new(Mutex::new(EventStoreFake::new()));
    let state = Arc::new(Mutex::new(StateFake::new()));
    let sid = "s1";
    let tid = "t1";
    let turn = "r1";

    let mut seed: Vec<Event> = vec![
        Event {
            id: String::new(),
            session_id: sid.into(),
            task_id: String::new(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            seq: 0,
            payload: EventPayload::SessionOpened(PayloadSessionOpened {
                principal: "user-42".into(),
                ..Default::default()
            }),
        },
        Event {
            id: String::new(),
            session_id: sid.into(),
            task_id: String::new(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            seq: 0,
            payload: EventPayload::UserSpoke(PayloadUserSpoke {
                text: "帮我订机票".into(),
            }),
        },
        Event {
            id: String::new(),
            session_id: sid.into(),
            task_id: tid.into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            seq: 0,
            payload: EventPayload::TaskCreated(PayloadTaskCreated {
                goal: "帮我订机票".into(),
                ..Default::default()
            }),
        },
        Event {
            id: String::new(),
            session_id: sid.into(),
            task_id: tid.into(),
            turn_id: turn.into(),
            ts: None,
            caused_by: String::new(),
            seq: 0,
            payload: EventPayload::TurnStarted(PayloadTurnStarted { index: 0 }),
        },
        Event {
            id: String::new(),
            session_id: sid.into(),
            task_id: tid.into(),
            turn_id: turn.into(),
            ts: None,
            caused_by: String::new(),
            seq: 0,
            payload: EventPayload::MemoryQueried(PayloadMemoryQueried {
                query: "帮 alice 订机票".into(),
                refs: refs.clone(),
            }),
        },
    ];
    store.lock().unwrap().append(&mut seed).unwrap();
    state.lock().unwrap().apply(&seed).unwrap();

    // 4. Assemble 应含 <memory_ref>
    let layered = LayeredContextEngine {
        state: state.clone(),
        store: Some(store.clone()),
        instructions: "You are an agent.".into(),
        tools: vec![],
    };
    let ctx = layered.assemble(sid, tid).unwrap();
    let has_memory = ctx
        .messages
        .iter()
        .any(|m| m.content.contains("<memory_ref"));
    assert!(has_memory, "Assemble output should contain <memory_ref>");

    // 5. 回放性
    let all = store.lock().unwrap().snapshot();
    let mut fresh = StateFake::new();
    fresh.apply(&all).unwrap();
    let v1 = state.lock().unwrap().view(sid).unwrap();
    let v2 = fresh.view(sid).unwrap();
    assert_eq!(v1.memory_refs.len(), v2.memory_refs.len());
    for i in 0..v1.memory_refs.len() {
        assert_eq!(v1.memory_refs[i].key, v2.memory_refs[i].key);
    }
}

#[test]
fn ch05_min_score_filter() {
    let mem = InMemStore::new();
    mem.upsert(MemoryItem {
        id: "a".into(),
        key: "a".into(),
        content: "苹果 apple".into(),
        embedding: embed_text("苹果 apple", EMBED_DIM),
        kind: MemoryKind::Semantic,
        version: 1,
        ..Default::default()
    })
    .unwrap();
    mem.upsert(MemoryItem {
        id: "b".into(),
        key: "b".into(),
        content: "订机票 book flight".into(),
        embedding: embed_text("订机票 book flight", EMBED_DIM),
        kind: MemoryKind::Semantic,
        version: 1,
        ..Default::default()
    })
    .unwrap();

    let refs = mem
        .query(&Query {
            semantic: "订机票".into(),
            top_k: 10,
            min_score: 0.9,
            ..Default::default()
        })
        .unwrap();

    for r in &refs {
        assert!(r.score >= 0.9, "score {} below MinScore 0.9", r.score);
    }
    let found_b = refs.iter().any(|r| r.key == "b");
    assert!(found_b, "expected 'b' in high-score results");
}

#[test]
fn ch05_version_regression_rejected() {
    let mem = InMemStore::new();
    mem.upsert(MemoryItem {
        id: "pref".into(),
        key: "user:42:diet".into(),
        content: "no_spicy".into(),
        kind: MemoryKind::Semantic,
        version: 2,
        ..Default::default()
    })
    .unwrap();

    let err = mem
        .upsert(MemoryItem {
            id: "pref".into(),
            key: "user:42:diet".into(),
            content: "spicy".into(),
            kind: MemoryKind::Semantic,
            version: 1,
            ..Default::default()
        })
        .expect_err("version regression should fail");
    assert!(err.0.contains("version regression"));
}
