//! TaskGraph —— 与 `runtime-go/domain/task_graph.go` 对齐。

use std::collections::HashMap;

use super::Task;

#[derive(Debug, Clone, Default)]
pub struct TaskGraph {
    pub roots: Vec<String>,
    pub children: HashMap<String, Vec<String>>,
}

pub fn build_task_graph(tasks: &HashMap<String, Task>) -> TaskGraph {
    let mut g = TaskGraph::default();
    for (id, t) in tasks {
        if t.parent_id.is_empty() {
            g.roots.push(id.clone());
        } else {
            g.children
                .entry(t.parent_id.clone())
                .or_default()
                .push(id.clone());
        }
    }
    g
}

impl TaskGraph {
    pub fn children_of(&self, parent_id: &str) -> &[String] {
        self.children
            .get(parent_id)
            .map(|v| v.as_slice())
            .unwrap_or(&[])
    }
}
