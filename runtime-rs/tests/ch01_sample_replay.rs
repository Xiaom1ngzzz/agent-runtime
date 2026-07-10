//! 与 `runtime-go/domain/ch01_sample_test.go` 对等的回放测试。
//! 三条断言逐字对齐:因果链完整、折叠视图正确、tokens_in 汇总 = 1830。
//!
//! 样本数据在 examples/ch01/sample.rs 中,由 example demo 与本测试共享。

#[path = "../examples/ch01/sample.rs"]
mod sample;

use std::collections::HashSet;

use agent_runtime_rs::domain::{TaskStatus, TurnStatus};
use agent_runtime_rs::event_payloads::EventPayload;

#[test]
fn ch01_sample_replay() {
    let events = sample::ch01_sample();

    // 1. 因果链完整:每个 caused_by 必须在此前的 Event 中出现过。
    let mut seen: HashSet<&str> = HashSet::new();
    for e in &events {
        if !e.caused_by.is_empty() && !seen.contains(e.caused_by.as_str()) {
            panic!("event {} references unknown caused_by={}", e.id, e.caused_by);
        }
        seen.insert(e.id.as_str());
    }

    // 2. 折叠视图正确:Task 成功、最后 Turn 是 r3(index=2)。
    let view = sample::fold_sample(&events);
    assert_eq!(view.session.id, "s1");
    assert_eq!(view.session.principal, "user-42");

    let task = view.tasks.get("t1").expect("task t1 missing");
    assert_eq!(task.status, TaskStatus::Succeeded);

    let last = view.last_turn.get("t1").expect("last turn missing");
    assert_eq!(last.id, "r3");
    assert_eq!(last.index, 2);
    assert_eq!(last.status, TurnStatus::Done);

    // 3. tokens_in 汇总: 520 + 610 + 700 = 1830
    let mut total_in = 0i64;
    for e in &events {
        if let EventPayload::TurnEnded(p) = &e.payload {
            total_in += p.tokens_in;
        }
    }
    assert_eq!(total_in, 1830);
}
