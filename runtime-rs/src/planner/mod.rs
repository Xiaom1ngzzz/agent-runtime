//! Planner —— 与 `runtime-go/planner/` 对齐。见 ch07-planner.md。

use crate::domain::{
    build_task_graph, Event, EventPayload, PayloadProgressUpdated, PayloadSubTaskSpawned,
    PayloadTaskCreated, PayloadTaskEnded, Progress, SessionView, Step, StepKind, Task, TaskStatus,
};

#[derive(Debug)]
pub struct PlannerError(pub String);

pub trait Planner {
    fn plan(&self, view: &SessionView, task_id: &str) -> Result<Vec<Event>, PlannerError>;
}

pub struct GraphPlanner;

impl Default for GraphPlanner {
    fn default() -> Self {
        Self
    }
}

impl GraphPlanner {
    pub fn new() -> Self {
        Self
    }
}

pub fn split_goals(goal: &str) -> Vec<String> {
    let parts: Vec<String> = goal
        .split(" + ")
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect();
    if parts.len() < 2 {
        Vec::new()
    } else {
        parts
    }
}

impl Planner for GraphPlanner {
    fn plan(&self, view: &SessionView, task_id: &str) -> Result<Vec<Event>, PlannerError> {
        let task = view
            .tasks
            .get(task_id)
            .ok_or_else(|| PlannerError(format!("unknown task {task_id}")))?
            .clone();
        let g = build_task_graph(&view.tasks);
        let children = g.children_of(task_id);
        let goals = split_goals(&task.goal);
        if goals.len() >= 2 && children.is_empty() {
            return Ok(spawn_children(&task, &goals));
        }
        let prog = build_progress_from_view(view, task_id);
        Ok(vec![Event {
            id: String::new(),
            session_id: String::new(),
            task_id: task_id.into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::ProgressUpdated(PayloadProgressUpdated {
                task_id: task_id.into(),
                progress: prog,
            }),
            seq: 0,
        }])
    }
}

fn spawn_children(parent: &Task, goals: &[String]) -> Vec<Event> {
    let n = goals.len() as i64;
    let mut share = if n > 0 {
        parent.budget.max_tokens / n
    } else {
        0
    };
    if share < 1 && parent.budget.max_tokens > 0 {
        share = 1;
    }
    let mut out = Vec::with_capacity(goals.len() * 2);
    for (i, goal) in goals.iter().enumerate() {
        let child_id = format!("{}.s{}", parent.id, i + 1);
        let mut budget = parent.budget;
        budget.max_tokens = share;
        out.push(Event {
            id: String::new(),
            session_id: String::new(),
            task_id: parent.id.clone(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::SubTaskSpawned(PayloadSubTaskSpawned {
                parent_task_id: parent.id.clone(),
                child_task_id: child_id.clone(),
                goal: goal.clone(),
                budget,
            }),
            seq: 0,
        });
        out.push(Event {
            id: String::new(),
            session_id: String::new(),
            task_id: child_id.clone(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskCreated(PayloadTaskCreated {
                goal: goal.clone(),
                budget,
                parent_id: parent.id.clone(),
            }),
            seq: 0,
        });
    }
    out
}

pub fn build_progress_from_view(view: &SessionView, task_id: &str) -> Progress {
    let task = view.tasks.get(task_id).cloned().unwrap_or_default();
    let prev = view.progresses.get(task_id).cloned().unwrap_or_default();
    let g = build_task_graph(&view.tasks);
    let children = g.children_of(task_id);
    let mut done = Vec::new();
    let mut next = Vec::new();
    for cid in children {
        let ct = view.tasks.get(cid).cloned().unwrap_or_default();
        let mut step = Step {
            intent: ct.goal.clone(),
            action: format!("subtask:{cid}"),
            kind: StepKind::Decision,
            ..Default::default()
        };
        match ct.status {
            TaskStatus::Succeeded => {
                step.observation = "succeeded".into();
                done.push(step);
            }
            TaskStatus::Failed | TaskStatus::Canceled | TaskStatus::Timeout => {
                step.observation = format!("{:?}", ct.status);
                step.kind = StepKind::Error;
                done.push(step);
            }
            _ => next.push(step),
        }
    }
    if children.is_empty() && !task.goal.is_empty() {
        next.push(Step {
            intent: task.goal.clone(),
            action: "execute".into(),
            kind: StepKind::Decision,
            ..Default::default()
        });
    }
    let mut ver = prev.version + 1;
    if ver < 1 {
        ver = 1;
    }
    Progress {
        goal: task.goal,
        done,
        next,
        open: prev.open,
        version: ver,
        updated_at: "planner".into(),
    }
}

pub struct SagaCoordinator;

impl SagaCoordinator {
    pub fn on_child_ended(
        &self,
        view: &SessionView,
        parent_id: &str,
    ) -> Result<Vec<Event>, PlannerError> {
        let parent = view
            .tasks
            .get(parent_id)
            .ok_or_else(|| PlannerError(format!("unknown parent {parent_id}")))?;
        if !matches!(parent.status, TaskStatus::Running | TaskStatus::Pending) {
            return Ok(vec![]);
        }
        let g = build_task_graph(&view.tasks);
        let children = g.children_of(parent_id);
        if children.is_empty() {
            return Ok(vec![]);
        }
        let mut all_done = true;
        let mut any_failed = false;
        for cid in children {
            match view.tasks.get(cid).map(|t| t.status) {
                Some(TaskStatus::Succeeded) => {}
                Some(TaskStatus::Failed | TaskStatus::Canceled | TaskStatus::Timeout) => {
                    any_failed = true;
                }
                _ => all_done = false,
            }
        }
        if !all_done {
            return Ok(vec![]);
        }
        let (status, reason) = if any_failed {
            (TaskStatus::Failed, "one or more children failed")
        } else {
            (TaskStatus::Succeeded, "all children succeeded")
        };
        Ok(vec![Event {
            id: String::new(),
            session_id: String::new(),
            task_id: parent_id.into(),
            turn_id: String::new(),
            ts: None,
            caused_by: String::new(),
            payload: EventPayload::TaskEnded(PayloadTaskEnded {
                status,
                reason: reason.into(),
            }),
            seq: 0,
        }])
    }
}
