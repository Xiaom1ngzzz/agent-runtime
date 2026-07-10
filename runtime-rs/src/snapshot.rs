//! Snapshot 与 SnapshotStore —— 与 `runtime-go/state/snapshot.go` 对齐。
//!
//! 参见 ch03 §3.6:Snapshot 是加速器,不是替代事件流。
//! 在 Turn 边界拍;丢了 Snapshot 从零 Fold 也必须能重建正确的 View。

use std::collections::HashMap;

use crate::domain::SessionView;
use crate::state::StateError;

/// "折叠到 seq 为止的 View"的镜像。
#[derive(Debug, Clone, Default)]
pub struct Snapshot {
    pub seq: i64,
    pub view: SessionView,
}

/// 存取每个 session 的最新 Snapshot。生产实现可以走 Postgres / KV;
/// 这里给内存 fake 供 ch03 端到端测试用。
pub trait SnapshotStore {
    fn latest(&self, session_id: &str) -> Result<Option<Snapshot>, StateError>;
    fn save(&mut self, session_id: &str, snap: Snapshot) -> Result<(), StateError>;
}

/// 最简内存实现,§3.4.3 L1 档次。
pub struct MemSnapshotStore {
    snaps: HashMap<String, Snapshot>,
}

impl MemSnapshotStore {
    pub fn new() -> Self {
        Self { snaps: HashMap::new() }
    }
}

impl Default for MemSnapshotStore {
    fn default() -> Self { Self::new() }
}

impl SnapshotStore for MemSnapshotStore {
    fn latest(&self, session_id: &str) -> Result<Option<Snapshot>, StateError> {
        Ok(self.snaps.get(session_id).cloned())
    }
    fn save(&mut self, session_id: &str, snap: Snapshot) -> Result<(), StateError> {
        self.snaps.insert(session_id.into(), snap);
        Ok(())
    }
}
