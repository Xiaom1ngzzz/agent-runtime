//! ch08 ToolExecutor —— 与 Go `executor/ch08_executor_test.go` 对齐。

#[path = "../examples/ch02/fakes.rs"]
mod fakes;

use std::sync::{Arc, Mutex};

use agent_runtime_rs::domain::{
    Event, EventPayload, PayloadLLMReplied, Tool, ToolCall, Turn,
};
use agent_runtime_rs::executor::{Executor, Registry, SnapshotStore, ToolExecutor, ToolFn};
use agent_runtime_rs::state::EventStore as _;
use fakes::EventStoreFake;

impl SnapshotStore for EventStoreFake {
    fn snapshot_all(&self) -> Vec<Event> {
        self.snapshot()
    }
}

#[test]
fn ch08_tool_executor() {
    let store = Arc::new(Mutex::new(EventStoreFake::new()));
    let reg = Arc::new(Registry::new());
    reg.register(
        Tool {
            name: "weather".into(),
            ..Default::default()
        },
        Arc::new(|_args: &str| Ok(r#"{"temp":26}"#.into())) as ToolFn,
    );

    let ex = ToolExecutor::new(store.clone(), reg);
    let mut e = Event {
        id: String::new(),
        session_id: "s1".into(),
        task_id: "t1".into(),
        turn_id: "r1".into(),
        ts: None,
        caused_by: String::new(),
        payload: EventPayload::LLMReplied(PayloadLLMReplied {
            assistant: Default::default(),
            tool_calls: vec![
                ToolCall {
                    id: "c1".into(),
                    name: "weather".into(),
                    arguments: r#"{"city":"BJ"}"#.into(),
                },
                ToolCall {
                    id: "c2".into(),
                    name: "nope".into(),
                    arguments: "{}".into(),
                },
            ],
            tokens_in: 1,
            tokens_out: 1,
        }),
        seq: 0,
    };
    {
        let mut buf = [e];
        store.lock().unwrap().append(&mut buf).unwrap();
        e = buf[0].clone();
        let _ = e;
    }

    let evs = ex
        .run(&Turn {
            id: "r1".into(),
            task_id: "t1".into(),
            ..Default::default()
        })
        .unwrap();

    let mut weather_ok = false;
    let mut bind_fail = false;
    for ev in &evs {
        match &ev.payload {
            EventPayload::ToolReturned(p) if p.call_id == "c1" && !p.is_error => weather_ok = true,
            EventPayload::ToolBindFailed(p) if p.name == "nope" => bind_fail = true,
            _ => {}
        }
    }
    assert!(weather_ok);
    assert!(bind_fail);
}
