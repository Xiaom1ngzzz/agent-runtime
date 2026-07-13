// Package state / snapshot.go —— Snapshot 结构与 SnapshotStore 接口。
//
// 参见 ch03 §3.6:Snapshot 是加速器,不是替代事件流。
// 在 Turn 边界拍;丢了 Snapshot 从零 Fold 也必须能重建正确的 View。
package state

import (
	"sync"

	"agent-runtime-go/domain"
)

// Snapshot 是"折叠到 Seq 为止的 View"的镜像。
// 恢复流程:View = latestSnapshot.View, 再 replay EventStore.LoadFrom(sessionID, Seq+1)。
type Snapshot struct {
	Seq  int64
	View domain.SessionView
}

// SnapshotStore 存取每个 session 的最新 Snapshot。
// 生产实现可以走 Postgres / KV;这里给内存 fake 供 ch03 端到端测试用。
type SnapshotStore interface {
	Latest(sessionID string) (Snapshot, bool, error)
	Save(sessionID string, snap Snapshot) error
}

// MemSnapshotStore 是最简内存实现,§3.4.3 L1 档次。
type MemSnapshotStore struct {
	mu    sync.Mutex
	snaps map[string]Snapshot
}

func NewMemSnapshotStore() *MemSnapshotStore {
	return &MemSnapshotStore{snaps: map[string]Snapshot{}}
}

func (s *MemSnapshotStore) Latest(sessionID string) (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snaps[sessionID]
	return snap, ok, nil
}

func (s *MemSnapshotStore) Save(sessionID string, snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps[sessionID] = cloneSnap(snap)
	return nil
}

// cloneSnap 保证 SnapshotStore 内部持有独立副本,后续调用方修改 View 不会污染快照。
// SessionView 包含 map/slice,浅拷贝会共享底层——这里做一次深拷贝(含 ch04 字段)。
func cloneSnap(snap Snapshot) Snapshot {
	out := Snapshot{Seq: snap.Seq}
	out.View.Session = snap.View.Session
	out.View.MaxSeq = snap.View.MaxSeq
	out.View.Tasks = make(map[string]domain.Task, len(snap.View.Tasks))
	for k, v := range snap.View.Tasks {
		out.View.Tasks[k] = v
	}
	out.View.LastTurn = make(map[string]domain.Turn, len(snap.View.LastTurn))
	for k, v := range snap.View.LastTurn {
		out.View.LastTurn[k] = v
	}
	out.View.SeenIDs = make(map[string]bool, len(snap.View.SeenIDs))
	for k, v := range snap.View.SeenIDs {
		out.View.SeenIDs[k] = v
	}
	if snap.View.WorkingSet != nil {
		out.View.WorkingSet = append([]domain.TurnDigest(nil), snap.View.WorkingSet...)
	}
	if snap.View.Summaries != nil {
		out.View.Summaries = make(map[int64]domain.Summary, len(snap.View.Summaries))
		for k, v := range snap.View.Summaries {
			out.View.Summaries[k] = v
		}
	}
	if snap.View.MemoryRefs != nil {
		out.View.MemoryRefs = append([]domain.MemoryRef(nil), snap.View.MemoryRefs...)
	}
	if snap.View.Progresses != nil {
		out.View.Progresses = make(map[string]domain.Progress, len(snap.View.Progresses))
		for k, v := range snap.View.Progresses {
			out.View.Progresses[k] = v
		}
	}
	return out
}
