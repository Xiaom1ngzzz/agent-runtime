//! 第一章 §1.6 用的黄金 Event 流："查天气 + 发邮件"。
//! 与 `runtime-go/domain/ch01_sample.go` 逐条对齐。
//!
//! 此文件同时被 examples/ch01/main.rs 和 tests/ch01_sample_replay.rs 复用
//! (集成测试通过 `#[path]` 引用),单一数据源、无重复。

#![allow(dead_code)]

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use agent_runtime_rs::domain::{
    Budget, Event, EventPayload, Message, PayloadLLMReplied, PayloadLLMRequested,
    PayloadSessionOpened, PayloadTaskCreated, PayloadTaskEnded, PayloadToolCalled,
    PayloadToolReturned, PayloadTurnEnded, PayloadTurnStarted, PayloadUserSpoke, SessionView,
    Task, TaskStatus, ToolCall, Turn, TurnStatus,
};

/// 生成第一章 §1.6 的样本 Event 流。
/// 结构固定,用于教学与测试;不要在此新增 Event。
pub fn ch01_sample() -> Vec<Event> {
    // 与 Go 版一致的基准时间: 2026-07-09 10:00:00 UTC
    // 2026-07-09 UTC = UNIX 1_783_591_200
    let t0 = UNIX_EPOCH + Duration::from_secs(1_783_591_200);
    let at = |offset_sec: u64| t0 + Duration::from_secs(offset_sec);

    let sid = "s1";
    let tid = "t1";
    let r1 = "r1";
    let r2 = "r2";
    let r3 = "r3";

    fn ev(
        id: &str,
        sid: &str,
        tid: &str,
        turn: &str,
        ts: SystemTime,
        caused_by: &str,
        payload: EventPayload,
    ) -> Event {
        Event {
            id: id.into(),
            session_id: sid.into(),
            task_id: tid.into(),
            turn_id: turn.into(),
            ts: Some(ts),
            caused_by: caused_by.into(),
            seq: 0,
            payload,
        }
    }

    vec![
        ev("e01", sid, "", "", at(0), "",
            EventPayload::SessionOpened(PayloadSessionOpened {
                principal: "user-42".into(),
                metadata: Default::default(),
            })),
        ev("e02", sid, "", "", at(1), "e01",
            EventPayload::UserSpoke(PayloadUserSpoke {
                text: "帮我查一下明天北京的天气，然后写一封提醒邮件给 alice@example.com".into(),
            })),
        ev("e03", sid, tid, "", at(1), "e02",
            EventPayload::TaskCreated(PayloadTaskCreated {
                goal: "查天气 + 发邮件".into(),
                budget: Budget { max_tokens: 8000, ..Default::default() },
                parent_id: String::new(),
            })),

        // ---- Turn 1: 决定查天气 ----
        ev("e04", sid, tid, r1, at(2), "",
            EventPayload::TurnStarted(PayloadTurnStarted { index: 0 })),
        ev("e05", sid, tid, r1, at(2), "",
            EventPayload::LLMRequested(PayloadLLMRequested {
                model: "claude-opus-4-7".into(), ..Default::default()
            })),
        ev("e06", sid, tid, r1, at(3), "e05",
            EventPayload::LLMReplied(PayloadLLMReplied {
                assistant: Message { role: "assistant".into(), ..Default::default() },
                tool_calls: vec![ToolCall {
                    id: "c1".into(), name: "weather".into(),
                    arguments: r#"{"city":"北京","date":"2026-07-10"}"#.into(),
                }],
                tokens_in: 520, tokens_out: 48,
            })),
        ev("e07", sid, tid, r1, at(3), "e06",
            EventPayload::ToolCalled(PayloadToolCalled {
                call_id: "c1".into(), name: "weather".into(),
                arguments: r#"{"city":"北京","date":"2026-07-10"}"#.into(),
            })),
        ev("e08", sid, tid, r1, at(4), "e07",
            EventPayload::ToolReturned(PayloadToolReturned {
                call_id: "c1".into(),
                content: r#"{"temp":26,"sky":"多云"}"#.into(),
                is_error: false,
            })),
        ev("e09", sid, tid, r1, at(4), "",
            EventPayload::TurnEnded(PayloadTurnEnded {
                status: TurnStatus::Done,
                tokens_in: 520, tokens_out: 48, latency_ms: 2100, ..Default::default()
            })),

        // ---- Turn 2: 决定发邮件 ----
        ev("e10", sid, tid, r2, at(5), "",
            EventPayload::TurnStarted(PayloadTurnStarted { index: 1 })),
        ev("e11", sid, tid, r2, at(5), "",
            EventPayload::LLMRequested(PayloadLLMRequested {
                model: "claude-opus-4-7".into(), ..Default::default()
            })),
        ev("e12", sid, tid, r2, at(6), "e11",
            EventPayload::LLMReplied(PayloadLLMReplied {
                assistant: Message { role: "assistant".into(), ..Default::default() },
                tool_calls: vec![ToolCall {
                    id: "c2".into(), name: "send_email".into(),
                    arguments: r#"{"to":"alice@example.com","body":"..."}"#.into(),
                }],
                tokens_in: 610, tokens_out: 72,
            })),
        ev("e13", sid, tid, r2, at(6), "e12",
            EventPayload::ToolCalled(PayloadToolCalled {
                call_id: "c2".into(), name: "send_email".into(),
                arguments: r#"{"to":"alice@example.com","body":"..."}"#.into(),
            })),
        ev("e14", sid, tid, r2, at(7), "e13",
            EventPayload::ToolReturned(PayloadToolReturned {
                call_id: "c2".into(),
                content: r#"{"ok":true}"#.into(),
                is_error: false,
            })),
        ev("e15", sid, tid, r2, at(7), "",
            EventPayload::TurnEnded(PayloadTurnEnded {
                status: TurnStatus::Done,
                tokens_in: 610, tokens_out: 72, latency_ms: 1800, ..Default::default()
            })),

        // ---- Turn 3: 收尾 ----
        ev("e16", sid, tid, r3, at(8), "",
            EventPayload::TurnStarted(PayloadTurnStarted { index: 2 })),
        ev("e17", sid, tid, r3, at(9), "",
            EventPayload::LLMReplied(PayloadLLMReplied {
                assistant: Message {
                    role: "assistant".into(),
                    content: "已经发送提醒邮件给 Alice。".into(),
                    ..Default::default()
                },
                tokens_in: 700, tokens_out: 20,
                ..Default::default()
            })),
        ev("e18", sid, tid, r3, at(9), "",
            EventPayload::TurnEnded(PayloadTurnEnded {
                status: TurnStatus::Done,
                tokens_in: 700, tokens_out: 20, latency_ms: 900, ..Default::default()
            })),

        ev("e19", sid, tid, "", at(9), "e17",
            EventPayload::TaskEnded(PayloadTaskEnded {
                status: TaskStatus::Succeeded, reason: String::new(),
            })),
    ]
}

/// 折叠 Ch01 样本流为 SessionView。
/// 这是 ch03 State.apply 的最小雏形——只覆盖样本用到的 payload。
pub fn fold_sample(events: &[Event]) -> SessionView {
    let mut view = SessionView::default();
    for e in events {
        match &e.payload {
            EventPayload::SessionOpened(p) => {
                view.session.id = e.session_id.clone();
                view.session.principal = p.principal.clone();
                view.session.created_at = e.ts;
                view.session.last_active_at = e.ts;
            }
            EventPayload::TaskCreated(p) => {
                view.tasks.insert(
                    e.task_id.clone(),
                    Task {
                        id: e.task_id.clone(),
                        session_id: e.session_id.clone(),
                        parent_id: p.parent_id.clone(),
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
                if let Some(t) = view.last_turn.get_mut(&e.task_id) {
                    t.status = p.status;
                    t.tokens_in = p.tokens_in;
                    t.tokens_out = p.tokens_out;
                    t.cost_us = p.cost_us;
                    t.latency_ms = p.latency_ms;
                }
            }
            _ => {}
        }
        if e.ts.is_some() {
            view.session.last_active_at = e.ts;
        }
    }
    view
}
