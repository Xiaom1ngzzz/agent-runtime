//! ch07 Task Graph Plan —— 与 `runtime-go/planner/ch07_task_graph_test.go` 对齐。

#[path = "../examples/ch02/fakes.rs"]
mod fakes;

use agent_runtime_rs::domain::{
    build_task_graph, Budget, Event, EventPayload, PayloadSessionOpened, PayloadTaskCreated,
    PayloadTaskEnded, TaskStatus,
};
use agent_runtime_rs::planner::{GraphPlanner, Planner, SagaCoordinator};
use agent_runtime_rs::state::{EventStore as _, State as _};
use fakes::{EventStoreFake, StateFake};

fn append(store: &mut EventStoreFake, state: &mut StateFake, sid: &str, mut e: Event) {
    e.session_id = sid.into();
    let mut buf = [e];
    store.append(&mut buf).unwrap();
    state.apply(&buf).unwrap();
}

#[test]
fn ch07_task_graph_plan() {
    let mut store = EventStoreFake::new();
    let mut state = StateFake::new();
    let sid = "s1";
    let parent = "t1";

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
            task_id: parent.into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskCreated(PayloadTaskCreated {
                goal: "查天气 + 发邮件".into(),
                budget: Budget {
                    max_tokens: 8000,
                    ..Default::default()
                },
                parent_id: String::new(),
            }),
            seq: 0,
        },
    );

    let planner = GraphPlanner::new();
    let view = state.view(sid).unwrap();
    let planned = planner.plan(&view, parent).unwrap();
    assert_eq!(planned.len(), 4);
    for e in planned {
        append(&mut store, &mut state, sid, e);
    }

    let view = state.view(sid).unwrap();
    let g = build_task_graph(&view.tasks);
    assert_eq!(g.roots, vec![parent.to_string()]);
    let children = g.children_of(parent).to_vec();
    assert_eq!(children.len(), 2);
    for cid in &children {
        assert_eq!(view.tasks[cid].parent_id, parent);
        assert_eq!(view.tasks[cid].budget.max_tokens, 4000);
    }

    for cid in children {
        append(
            &mut store,
            &mut state,
            sid,
            Event {
                id: String::new(),
                session_id: String::new(),
                task_id: cid,
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
    }
    let view = state.view(sid).unwrap();
    let ended = SagaCoordinator.on_child_ended(&view, parent).unwrap();
    assert_eq!(ended.len(), 1);
    for e in ended {
        append(&mut store, &mut state, sid, e);
    }
    let view = state.view(sid).unwrap();
    assert_eq!(view.tasks[parent].status, TaskStatus::Succeeded);

    let prog = planner.plan(&view, parent).unwrap();
    assert!(matches!(
        prog[0].payload,
        EventPayload::ProgressUpdated(ref p) if p.progress.done.len() == 2
    ));
    for event in prog {
        append(&mut store, &mut state, sid, event);
    }
    let view = state.view(sid).unwrap();
    assert!(planner.plan(&view, parent).unwrap().is_empty());
}

#[test]
fn ch07_child_budget_never_exceeds_parent() {
    let planner = GraphPlanner::new();
    let mut view = agent_runtime_rs::domain::SessionView::default();
    view.tasks.insert(
        "p".into(),
        agent_runtime_rs::domain::Task {
            id: "p".into(),
            goal: "A + B + C".into(),
            budget: Budget {
                max_tokens: 2,
                ..Default::default()
            },
            ..Default::default()
        },
    );
    let events = planner.plan(&view, "p").unwrap();
    let total: i64 = events
        .iter()
        .filter_map(|event| match &event.payload {
            EventPayload::TaskCreated(payload) => Some(payload.budget.max_tokens),
            _ => None,
        })
        .sum();
    assert_eq!(total, 2);
}

#[test]
fn ch07_repairs_partially_appended_plan() {
    let planner = GraphPlanner::new();
    let mut view = agent_runtime_rs::domain::SessionView::default();
    view.tasks.insert(
        "p".into(),
        agent_runtime_rs::domain::Task {
            id: "p".into(),
            goal: "A + B".into(),
            budget: Budget {
                max_tokens: 4,
                ..Default::default()
            },
            ..Default::default()
        },
    );
    view.tasks.insert(
        "p.s1".into(),
        agent_runtime_rs::domain::Task {
            id: "p.s1".into(),
            parent_id: "p".into(),
            goal: "A".into(),
            status: TaskStatus::Running,
            ..Default::default()
        },
    );
    let events = planner.plan(&view, "p").unwrap();
    assert_eq!(events.len(), 2);
    assert!(matches!(events[0].payload, EventPayload::SubTaskSpawned(_)));
    assert!(matches!(events[1].payload, EventPayload::TaskCreated(_)));
}

#[test]
fn ch07_split_goals() {
    assert!(agent_runtime_rs::planner::split_goals("单一目标").is_empty());
    let g = agent_runtime_rs::planner::split_goals("查天气 + 发邮件");
    assert_eq!(g, vec!["查天气", "发邮件"]);
}
