//! ch10 Eval —— 与 Go `eval/ch10_eval_test.go` 对齐。

use agent_runtime_rs::domain::{
    Event, EventPayload, PayloadProgressUpdated, PayloadTaskCreated, PayloadTaskEnded,
    PayloadToolCalled, PayloadToolReturned, TaskStatus,
};
use agent_runtime_rs::eval::compare_streams;

#[test]
fn ch10_compare_streams() {
    let golden = vec![
        Event {
            id: "1".into(),
            session_id: "s".into(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskCreated(PayloadTaskCreated {
                goal: "g".into(),
                ..Default::default()
            }),
            seq: 1,
        },
        Event {
            id: "2".into(),
            session_id: "s".into(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::ToolCalled(PayloadToolCalled {
                call_id: "c1".into(),
                name: "weather".into(),
                arguments: String::new(),
            }),
            seq: 2,
        },
        Event {
            id: "3".into(),
            session_id: "s".into(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::ToolReturned(PayloadToolReturned {
                call_id: "c1".into(),
                content: "ok".into(),
                is_error: false,
            }),
            seq: 3,
        },
        Event {
            id: "4".into(),
            session_id: "s".into(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskEnded(PayloadTaskEnded {
                status: TaskStatus::Succeeded,
                reason: String::new(),
            }),
            seq: 4,
        },
    ];
    let s = compare_streams(&golden, &golden, "t1");
    assert!(s.passed);
    assert_eq!(s.tool_calls, 1);

    let mut bad = golden.clone();
    if let EventPayload::TaskEnded(p) = &mut bad[3].payload {
        p.status = TaskStatus::Failed;
    }
    let s2 = compare_streams(&golden, &bad, "t1");
    assert!(!s2.passed);

    let mut replaced = golden.clone();
    replaced[1].payload = EventPayload::ProgressUpdated(PayloadProgressUpdated {
        task_id: "t1".into(),
        ..Default::default()
    });
    replaced[2].payload = EventPayload::ProgressUpdated(PayloadProgressUpdated {
        task_id: "t1".into(),
        ..Default::default()
    });
    let s3 = compare_streams(&golden, &replaced, "t1");
    assert!(!s3.passed);
    assert!(!s3.event_sequence_match);

    assert!(!compare_streams(&[], &[], "t1").passed);
}
