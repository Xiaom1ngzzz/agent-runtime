//! Eval —— 与 `runtime-go/eval/` 对齐。见 ch10-eval.md。

use std::collections::{HashMap, HashSet};

use crate::domain::{Event, EventPayload, SessionView, TaskStatus};

#[derive(Debug, Clone, Default)]
pub struct Metrics {
    pub event_count: usize,
    pub tokens_in: i64,
    pub tokens_out: i64,
    pub tool_calls: i64,
    pub tool_errors: i64,
}

#[derive(Debug, Clone, Default)]
pub struct Score {
    pub event_count_match: bool,
    pub event_sequence_match: bool,
    pub final_task_status: TaskStatus,
    pub found_terminal: bool,
    pub status_match: bool,
    pub golden: Metrics,
    pub actual: Metrics,
    pub token_delta_in: i64,
    pub token_delta_out: i64,
    pub tokens_in: i64,
    pub tokens_out: i64,
    pub tool_calls: i64,
    pub tool_errors: i64,
    pub tool_error_rate: f64,
    pub passed: bool,
    pub notes: Vec<String>,
}

pub fn compare_streams(golden: &[Event], actual: &[Event], task_id: &str) -> Score {
    let golden_filtered = filter_task(golden, task_id);
    let actual_filtered = filter_task(actual, task_id);
    let g = summarize(&golden_filtered);
    let a = summarize(&actual_filtered);
    let mut s = Score {
        event_count_match: g.metrics.event_count == a.metrics.event_count,
        event_sequence_match: g.fingerprints == a.fingerprints,
        final_task_status: a.final_status.unwrap_or_default(),
        found_terminal: a.final_status.is_some(),
        status_match: g.final_status.is_some() && g.final_status == a.final_status,
        golden: g.metrics.clone(),
        actual: a.metrics.clone(),
        token_delta_in: a.metrics.tokens_in - g.metrics.tokens_in,
        token_delta_out: a.metrics.tokens_out - g.metrics.tokens_out,
        tokens_in: a.metrics.tokens_in,
        tokens_out: a.metrics.tokens_out,
        tool_calls: a.metrics.tool_calls,
        tool_errors: a.metrics.tool_errors,
        ..Default::default()
    };
    if !s.event_count_match {
        s.notes.push("event count mismatch".into());
    }
    if !s.event_sequence_match {
        s.notes
            .push("event sequence or key payload mismatch".into());
    }
    if g.final_status.is_none() {
        s.notes.push("golden terminal task status missing".into());
    }
    if a.final_status.is_none() {
        s.notes.push("actual terminal task status missing".into());
    }
    if s.actual.tool_calls > 0 {
        s.tool_error_rate = s.actual.tool_errors as f64 / s.actual.tool_calls as f64;
    } else if s.actual.tool_errors > 0 {
        s.tool_error_rate = 1.0;
    }
    if !s.status_match {
        s.notes.push("final task status mismatch".into());
    }
    if s.golden.tool_calls != s.actual.tool_calls {
        s.notes.push("tool call count mismatch".into());
    }
    if s.golden.tool_errors != s.actual.tool_errors {
        s.notes.push("tool error count mismatch".into());
    }
    s.passed = s.event_count_match
        && s.event_sequence_match
        && s.status_match
        && s.golden.tool_calls == s.actual.tool_calls
        && s.golden.tool_errors == s.actual.tool_errors;
    s
}

struct StreamSummary {
    metrics: Metrics,
    fingerprints: Vec<String>,
    final_status: Option<TaskStatus>,
}

fn filter_task<'a>(events: &'a [Event], task_id: &str) -> Vec<&'a Event> {
    if task_id.is_empty() {
        return events.iter().collect();
    }
    events.iter().filter(|e| e.task_id == task_id).collect()
}

fn summarize(events: &[&Event]) -> StreamSummary {
    let mut out = StreamSummary {
        metrics: Metrics {
            event_count: events.len(),
            ..Default::default()
        },
        fingerprints: Vec::with_capacity(events.len()),
        final_status: None,
    };
    let mut call_order = HashMap::new();
    let mut failed_calls = HashSet::new();
    let mut next_call = 0usize;
    for (index, e) in events.iter().enumerate() {
        out.fingerprints
            .push(event_fingerprint(e, &mut call_order, &mut next_call));
        match &e.payload {
            EventPayload::TaskEnded(p) => out.final_status = Some(p.status),
            EventPayload::TurnEnded(p) => {
                out.metrics.tokens_in += p.tokens_in;
                out.metrics.tokens_out += p.tokens_out;
            }
            EventPayload::ToolCalled(p) => {
                out.metrics.tool_calls += 1;
                canonical_call(&p.call_id, &mut call_order, &mut next_call);
            }
            EventPayload::ToolReturned(p) if p.is_error => {
                failed_calls.insert(error_key(&p.call_id, index));
            }
            EventPayload::ToolBindFailed(p) => {
                failed_calls.insert(error_key(&p.call_id, index));
            }
            _ => {}
        }
    }
    out.metrics.tool_errors = failed_calls.len() as i64;
    out
}

fn event_fingerprint(
    e: &Event,
    call_order: &mut HashMap<String, usize>,
    next_call: &mut usize,
) -> String {
    match &e.payload {
        EventPayload::TaskCreated(p) => {
            format!("TaskCreated|goal={}|parent={}", p.goal, p.parent_id)
        }
        EventPayload::TaskEnded(p) => format!("TaskEnded|status={:?}", p.status),
        EventPayload::TurnEnded(p) => format!("TurnEnded|status={:?}", p.status),
        EventPayload::ToolCalled(p) => format!(
            "ToolCalled|call={}|name={}|args={}",
            canonical_call(&p.call_id, call_order, next_call),
            p.name,
            normalize_json(&p.arguments)
        ),
        EventPayload::ToolReturned(p) => format!(
            "ToolReturned|call={}|error={}",
            canonical_call(&p.call_id, call_order, next_call),
            p.is_error
        ),
        EventPayload::ToolBindFailed(p) => format!(
            "ToolBindFailed|call={}|name={}|reason={}",
            canonical_call(&p.call_id, call_order, next_call),
            p.name,
            p.reason
        ),
        other => format!("{:?}", std::mem::discriminant(other)),
    }
}

fn canonical_call(id: &str, order: &mut HashMap<String, usize>, next: &mut usize) -> usize {
    if let Some(index) = order.get(id) {
        return *index;
    }
    let index = *next;
    order.insert(id.into(), index);
    *next += 1;
    index
}

fn normalize_json(raw: &str) -> String {
    serde_json::from_str::<serde_json::Value>(raw)
        .and_then(|value| serde_json::to_string(&value))
        .unwrap_or_else(|_| raw.into())
}

fn error_key(call_id: &str, index: usize) -> String {
    if call_id.is_empty() {
        format!("orphan:{index}")
    } else {
        call_id.into()
    }
}

pub fn score_view(
    view: &SessionView,
    task_id: &str,
    want_status: TaskStatus,
    min_progress_ver: i64,
) -> Score {
    let task = view.tasks.get(task_id);
    let progress = view.progresses.get(task_id);
    let status = task.map(|t| t.status).unwrap_or_default();
    let prog_ver = progress.map(|p| p.version).unwrap_or(0);
    let mut s = Score {
        final_task_status: status,
        found_terminal: task.is_some_and(|t| is_terminal(t.status)),
        status_match: task.is_some_and(|t| is_terminal(t.status) && t.status == want_status),
        ..Default::default()
    };
    if task.is_none() {
        s.notes.push("task missing".into());
    } else if !s.found_terminal {
        s.notes.push("task is not terminal".into());
    }
    if progress.is_none() {
        s.notes.push("progress missing".into());
    }
    if prog_ver < min_progress_ver {
        s.notes.push("progress version too low".into());
    }
    s.passed = s.status_match && progress.is_some() && prog_ver >= min_progress_ver;
    s
}

fn is_terminal(status: TaskStatus) -> bool {
    matches!(
        status,
        TaskStatus::Succeeded | TaskStatus::Failed | TaskStatus::Canceled | TaskStatus::Timeout
    )
}
