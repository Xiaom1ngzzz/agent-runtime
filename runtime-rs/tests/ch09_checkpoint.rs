//! ch09 Checkpoint recover —— 与 Go `state/ch09_checkpoint_test.go` 对齐。

#[path = "../examples/ch02/fakes.rs"]
mod fakes;

use agent_runtime_rs::domain::{
    Event, EventPayload, PayloadProgressUpdated, PayloadSessionOpened, PayloadTaskCreated,
    PayloadTaskEnded, PayloadTurnEnded, PayloadTurnStarted, Progress, Step, StepKind, TaskStatus,
    TurnStatus,
};
use agent_runtime_rs::state::{
    recover, take_checkpoint, Checkpoint, CheckpointStore, EventStore as _, MemCheckpointStore,
    Snapshot, State as _, CHECKPOINT_SCHEMA_VERSION,
};
use fakes::{EventStoreFake, StateFake};

fn append(store: &mut EventStoreFake, state: &mut StateFake, sid: &str, mut e: Event) {
    e.session_id = sid.into();
    let mut buf = [e];
    store.append(&mut buf).unwrap();
    state.apply(&buf).unwrap();
}

#[test]
fn ch09_schema_mismatch_falls_back_to_full_replay() {
    let mut store = EventStoreFake::new();
    let mut state = StateFake::new();
    let mut cps = MemCheckpointStore::new();
    let sid = "s-schema";
    append(
        &mut store,
        &mut state,
        sid,
        Event {
            id: String::new(),
            session_id: String::new(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskCreated(PayloadTaskCreated {
                goal: "full replay".into(),
                ..Default::default()
            }),
            seq: 0,
        },
    );
    cps.save(
        sid,
        Checkpoint {
            schema_version: CHECKPOINT_SCHEMA_VERSION + 1,
            snapshot: Snapshot {
                seq: 999,
                ..Default::default()
            },
        },
    )
    .unwrap();
    assert_eq!(
        cps.latest(sid).unwrap().unwrap().schema_version,
        CHECKPOINT_SCHEMA_VERSION + 1
    );

    let mut fresh = StateFake::new();
    assert_eq!(recover(sid, &cps, &store, &mut fresh).unwrap(), 1);
}

#[test]
fn ch09_checkpoint_recover() {
    let mut store = EventStoreFake::new();
    let mut state = StateFake::new();
    let mut cps = MemCheckpointStore::new();
    let sid = "s1";

    append(
        &mut store,
        &mut state,
        sid,
        Event {
            id: String::new(),
            session_id: String::new(),
            task_id: String::new(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::SessionOpened(PayloadSessionOpened {
                principal: "u".into(),
                ..Default::default()
            }),
            seq: 0,
        },
    );
    append(
        &mut store,
        &mut state,
        sid,
        Event {
            id: String::new(),
            session_id: String::new(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskCreated(PayloadTaskCreated {
                goal: "demo".into(),
                budget: Default::default(),
                parent_id: String::new(),
            }),
            seq: 0,
        },
    );
    append(
        &mut store,
        &mut state,
        sid,
        Event {
            id: String::new(),
            session_id: String::new(),
            task_id: "t1".into(),
            turn_id: "r1".into(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TurnStarted(PayloadTurnStarted { index: 0 }),
            seq: 0,
        },
    );
    append(
        &mut store,
        &mut state,
        sid,
        Event {
            id: String::new(),
            session_id: String::new(),
            task_id: "t1".into(),
            turn_id: "r1".into(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TurnEnded(PayloadTurnEnded {
                status: TurnStatus::Done,
                tokens_in: 10,
                tokens_out: 2,
                ..Default::default()
            }),
            seq: 0,
        },
    );
    append(
        &mut store,
        &mut state,
        sid,
        Event {
            id: String::new(),
            session_id: String::new(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::ProgressUpdated(PayloadProgressUpdated {
                task_id: "t1".into(),
                progress: Progress {
                    goal: "demo".into(),
                    version: 1,
                    done: vec![Step {
                        intent: "step1".into(),
                        kind: StepKind::Decision,
                        ..Default::default()
                    }],
                    ..Default::default()
                },
            }),
            seq: 0,
        },
    );

    take_checkpoint(sid, &state, &mut cps).unwrap();

    append(
        &mut store,
        &mut state,
        sid,
        Event {
            id: String::new(),
            session_id: String::new(),
            task_id: "t1".into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskEnded(PayloadTaskEnded {
                status: TaskStatus::Succeeded,
                reason: String::new(),
            }),
            seq: 0,
        },
    );

    let mut fresh = StateFake::new();
    let n = recover(sid, &cps, &store, &mut fresh).unwrap();
    assert_eq!(n, 1);
    let got = fresh.view(sid).unwrap();
    assert_eq!(got.tasks["t1"].status, TaskStatus::Succeeded);
    assert_eq!(got.progresses["t1"].version, 1);
    assert_eq!(got.working_set.len(), 1);
    assert!(cps.latest(sid).unwrap().is_some());
}
