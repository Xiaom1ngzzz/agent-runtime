// Package state / checkpoint.go —— Checkpoint 与恢复(ch09)。
//
// Checkpoint = Turn 边界 Snapshot + 校验元数据。
// 恢复:Latest → LoadSnapshot → LoadFrom(seq)(返回 seq 之后的增量)→ Apply。
package state

import (
	"fmt"

	"agent-runtime-go/domain"
)

// Checkpoint 是可跨进程恢复的单元。Round 2 = Snapshot + SchemaVersion。
type Checkpoint struct {
	SchemaVersion int // 当前为 1;不匹配则丢弃,全量 replay
	Snapshot      Snapshot
}

const CheckpointSchemaVersion = 1

// CheckpointStore 在 SnapshotStore 之上加一层 schema 校验。
type CheckpointStore interface {
	Latest(sessionID string) (Checkpoint, bool, error)
	Save(sessionID string, cp Checkpoint) error
}

// MemCheckpointStore 包装 MemSnapshotStore。
type MemCheckpointStore struct {
	snaps *MemSnapshotStore
}

func NewMemCheckpointStore() *MemCheckpointStore {
	return &MemCheckpointStore{snaps: NewMemSnapshotStore()}
}

func (s *MemCheckpointStore) Latest(sessionID string) (Checkpoint, bool, error) {
	snap, ok, err := s.snaps.Latest(sessionID)
	if err != nil || !ok {
		return Checkpoint{}, ok, err
	}
	return Checkpoint{SchemaVersion: CheckpointSchemaVersion, Snapshot: snap}, true, nil
}

func (s *MemCheckpointStore) Save(sessionID string, cp Checkpoint) error {
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = CheckpointSchemaVersion
	}
	return s.snaps.Save(sessionID, cp.Snapshot)
}

// RecoverableState 是恢复所需的最小 State 能力。
type RecoverableState interface {
	State
	LoadSnapshot(sessionID string, view domain.SessionView)
}

// Recover 从 Checkpoint + EventStore 增量重建 State。
// 若 Checkpoint schema 不匹配或缺失,fromSeq=0 全量 Load。
func Recover(sessionID string, cps CheckpointStore, store EventStore, st RecoverableState) (replayed int, err error) {
	cp, ok, err := cps.Latest(sessionID)
	if err != nil {
		return 0, err
	}
	fromSeq := int64(0)
	if ok && cp.SchemaVersion == CheckpointSchemaVersion {
		st.LoadSnapshot(sessionID, cp.Snapshot.View)
		fromSeq = cp.Snapshot.Seq
	}
	remaining, err := store.LoadFrom(sessionID, fromSeq)
	if err != nil {
		return 0, err
	}
	if err := st.Apply(remaining); err != nil {
		return 0, fmt.Errorf("recover apply: %w", err)
	}
	return len(remaining), nil
}

// TakeCheckpoint 在 Turn 边界从当前 View 拍照。
func TakeCheckpoint(sessionID string, st State, cps CheckpointStore) error {
	view, err := st.View(sessionID)
	if err != nil {
		return err
	}
	return cps.Save(sessionID, Checkpoint{
		SchemaVersion: CheckpointSchemaVersion,
		Snapshot:      Snapshot{Seq: view.MaxSeq, View: view},
	})
}
