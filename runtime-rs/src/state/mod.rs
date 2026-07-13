//! State 与 EventStore 接口。
//! 与 `runtime-go/state/state.go` 对齐。实现见 ch03-state-event.md 与 ch09-checkpoint.md。

pub mod snapshot;
pub mod wire;

pub use snapshot::{MemSnapshotStore, Snapshot, SnapshotStore};
pub use wire::{marshal_event, unmarshal_event, EventWire};

use crate::domain::{Event, SessionView};

pub trait State {
    fn apply(&mut self, events: &[Event]) -> Result<(), StateError>;
    fn view(&self, session_id: &str) -> Result<SessionView, StateError>;
}

pub trait EventStore {
    /// events 传入 `&mut [Event]` 是为了让 store 把分配的 id / seq 写回。
    /// 对应 Go 端 slice 天然可变的语义。
    fn append(&mut self, events: &mut [Event]) -> Result<(), StateError>;
    fn load(&self, session_id: &str) -> Result<Vec<Event>, StateError>;
    /// 返回 seq > from_seq 的所有事件,按 seq 升序。from_seq=0 = 全量。§3.6
    fn load_from(&self, session_id: &str, from_seq: i64) -> Result<Vec<Event>, StateError>;
}

#[derive(Debug)]
pub struct StateError(pub String);
