//! Checkpoint 与恢复 —— 与 `runtime-go/state/checkpoint.go` 对齐。见 ch09。

use std::collections::HashMap;

use super::{EventStore, Snapshot, State, StateError};
use crate::domain::SessionView;

pub const CHECKPOINT_SCHEMA_VERSION: i32 = 1;

#[derive(Debug, Clone, Default)]
pub struct Checkpoint {
    pub schema_version: i32,
    pub snapshot: Snapshot,
}

pub trait CheckpointStore {
    fn latest(&self, session_id: &str) -> Result<Option<Checkpoint>, StateError>;
    fn save(&mut self, session_id: &str, cp: Checkpoint) -> Result<(), StateError>;
}

pub struct MemCheckpointStore {
    checkpoints: HashMap<String, Checkpoint>,
}

impl MemCheckpointStore {
    pub fn new() -> Self {
        Self {
            checkpoints: HashMap::new(),
        }
    }
}

impl Default for MemCheckpointStore {
    fn default() -> Self {
        Self::new()
    }
}

impl CheckpointStore for MemCheckpointStore {
    fn latest(&self, session_id: &str) -> Result<Option<Checkpoint>, StateError> {
        Ok(self.checkpoints.get(session_id).cloned())
    }

    fn save(&mut self, session_id: &str, mut cp: Checkpoint) -> Result<(), StateError> {
        if cp.schema_version == 0 {
            cp.schema_version = CHECKPOINT_SCHEMA_VERSION;
        }
        if let Some(existing) = self.checkpoints.get(session_id) {
            if existing.snapshot.seq > cp.snapshot.seq {
                return Err(StateError(format!(
                    "checkpoint save rejected: existing seq {} > new seq {}",
                    existing.snapshot.seq, cp.snapshot.seq
                )));
            }
        }
        self.checkpoints.insert(session_id.into(), cp);
        Ok(())
    }
}

pub trait RecoverableState: State {
    fn load_snapshot(&mut self, session_id: &str, view: SessionView);
    fn reset_session(&mut self, session_id: &str);
}

pub fn take_checkpoint(
    session_id: &str,
    state: &dyn State,
    cps: &mut dyn CheckpointStore,
) -> Result<(), StateError> {
    let view = state.view(session_id)?;
    cps.save(
        session_id,
        Checkpoint {
            schema_version: CHECKPOINT_SCHEMA_VERSION,
            snapshot: Snapshot {
                seq: view.max_seq,
                view,
            },
        },
    )
}

pub fn recover(
    session_id: &str,
    cps: &dyn CheckpointStore,
    store: &dyn EventStore,
    state: &mut dyn RecoverableState,
) -> Result<usize, StateError> {
    let mut from_seq = 0i64;
    if let Some(cp) = cps.latest(session_id)? {
        if cp.schema_version == CHECKPOINT_SCHEMA_VERSION {
            if cp.snapshot.seq != cp.snapshot.view.max_seq {
                return Err(StateError(format!(
                    "checkpoint seq/view mismatch: seq={} max_seq={}",
                    cp.snapshot.seq, cp.snapshot.view.max_seq
                )));
            }
            state.reset_session(session_id);
            state.load_snapshot(session_id, cp.snapshot.view);
            from_seq = cp.snapshot.seq;
        } else {
            state.reset_session(session_id);
        }
    } else {
        state.reset_session(session_id);
    }
    let remaining = store.load_from(session_id, from_seq)?;
    let n = remaining.len();
    state.apply(&remaining)?;
    Ok(n)
}
