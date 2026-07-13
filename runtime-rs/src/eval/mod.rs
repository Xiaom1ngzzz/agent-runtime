//! Eval —— 与 `runtime-go/eval/` 对齐。见 ch10-eval.md。

use crate::domain::{Event, EventPayload, SessionView, TaskStatus};

#[derive(Debug, Clone, Default)]
pub struct Score {
    pub event_count_match: bool,
    pub final_task_status: TaskStatus,
    pub status_match: bool,
    pub tokens_in: i64,
    pub tokens_out: i64,
    pub tool_calls: i64,
    pub tool_errors: i64,
    pub tool_error_rate: f64,
    pub passed: bool,
    pub notes: Vec<String>,
}

pub fn compare_streams(golden: &[Event], actual: &[Event], task_id: &str) -> Score {
    let mut s = Score {
        event_count_match: golden.len() == actual.len(),
        ..Default::default()
    };
    if !s.event_count_match {
        s.notes.push("event count mismatch".into());
    }
    let mut gold_status = TaskStatus::Pending;
    for e in golden {
        accumulate(&mut s, e, task_id, &mut gold_status);
    }
    s.tokens_in = 0;
    s.tokens_out = 0;
    s.tool_calls = 0;
    s.tool_errors = 0;
    let mut act_status = TaskStatus::Pending;
    for e in actual {
        accumulate(&mut s, e, task_id, &mut act_status);
    }
    s.final_task_status = act_status;
    s.status_match = gold_status == act_status;
    if s.tool_calls > 0 {
        s.tool_error_rate = s.tool_errors as f64 / s.tool_calls as f64;
    }
    s.passed = s.event_count_match && s.status_match && s.tool_error_rate == 0.0;
    if !s.status_match {
        s.notes.push("final task status mismatch".into());
    }
    s
}

fn accumulate(s: &mut Score, e: &Event, task_id: &str, status: &mut TaskStatus) {
    if !task_id.is_empty() && !e.task_id.is_empty() && e.task_id != task_id {
        return;
    }
    match &e.payload {
        EventPayload::TaskEnded(p) => *status = p.status,
        EventPayload::TurnEnded(p) => {
            s.tokens_in += p.tokens_in;
            s.tokens_out += p.tokens_out;
        }
        EventPayload::ToolCalled(_) => s.tool_calls += 1,
        EventPayload::ToolReturned(p) if p.is_error => s.tool_errors += 1,
        EventPayload::ToolBindFailed(_) => s.tool_errors += 1,
        _ => {}
    }
}

pub fn score_view(
    view: &SessionView,
    task_id: &str,
    want_status: TaskStatus,
    min_progress_ver: i64,
) -> Score {
    let status = view
        .tasks
        .get(task_id)
        .map(|t| t.status)
        .unwrap_or_default();
    let prog_ver = view
        .progresses
        .get(task_id)
        .map(|p| p.version)
        .unwrap_or(0);
    let mut s = Score {
        final_task_status: status,
        status_match: status == want_status,
        ..Default::default()
    };
    if prog_ver < min_progress_ver {
        s.notes.push("progress version too low".into());
    }
    s.passed = s.status_match && prog_ver >= min_progress_ver;
    s
}
