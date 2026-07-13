//! 第一章 §1.6 的可运行 demo。
//!
//! ```bash
//! cargo run --example ch01
//! ```
//!
//! 输出这次"查天气 + 发邮件"交互的 Event 流概览与折叠后的 SessionView。

mod sample;

use agent_runtime_rs::domain::EventPayload;

fn main() {
    let events = sample::ch01_sample();
    println!("== Event 流({}条) ==", events.len());
    for e in &events {
        let caused = if e.caused_by.is_empty() { "-" } else { e.caused_by.as_str() };
        println!(
            "  {:<3} {:<20} session={} task={:<3} turn={:<3} caused_by={}",
            e.id,
            e.payload.event_type(),
            e.session_id,
            if e.task_id.is_empty() { "-" } else { e.task_id.as_str() },
            if e.turn_id.is_empty() { "-" } else { e.turn_id.as_str() },
            caused,
        );
    }

    let view = sample::fold_sample(&events);
    println!();
    println!("== 折叠后的 SessionView ==");
    println!(
        "  session:  id={} principal={}",
        view.session.id, view.session.principal
    );
    for (tid, task) in &view.tasks {
        println!(
            "  task:     id={} goal={:?} status={:?}",
            tid, task.goal, task.status
        );
    }
    for (tid, turn) in &view.last_turn {
        println!(
            "  turn:     task={} id={} index={} status={:?} tokens_in={} tokens_out={}",
            tid, turn.id, turn.index, turn.status, turn.tokens_in, turn.tokens_out
        );
    }

    let total_in: i64 = events
        .iter()
        .filter_map(|e| match &e.payload {
            EventPayload::TurnEnded(p) => Some(p.tokens_in),
            _ => None,
        })
        .sum();
    println!("  total tokens_in: {}", total_in);
}
