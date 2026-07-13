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
// 恢复流程:View = latestSnapshot.View, 再 replay EventStore.LoadFrom(sessionID, Seq);
// LoadFrom 的契约是返回 seq > fromSeq 的事件。
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
	if !ok {
		return Snapshot{}, false, nil
	}
	return cloneSnap(snap), true, nil
}

func (s *MemSnapshotStore) Save(sessionID string, snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps[sessionID] = cloneSnap(snap)
	return nil
}

// CloneView 返回 SessionView 的深拷贝,供 View() 等只读 API 使用。
func CloneView(in domain.SessionView) domain.SessionView {
	return cloneSnap(Snapshot{View: in}).View
}

// cloneSnap 保证 SnapshotStore 内部持有独立副本,后续调用方修改 View 不会污染快照。
// SessionView 包含 map/slice,浅拷贝会共享底层——这里做一次深拷贝(含 ch04 字段)。
func cloneSnap(snap Snapshot) Snapshot {
	out := Snapshot{Seq: snap.Seq}
	out.View.Session = snap.View.Session
	if snap.View.Session.Metadata != nil {
		out.View.Session.Metadata = make(map[string]string, len(snap.View.Session.Metadata))
		for k, v := range snap.View.Session.Metadata {
			out.View.Session.Metadata[k] = v
		}
	}
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
			out.View.Summaries[k] = cloneSummary(v)
		}
	}
	if snap.View.MemoryRefs != nil {
		out.View.MemoryRefs = append([]domain.MemoryRef(nil), snap.View.MemoryRefs...)
	}
	if snap.View.Progresses != nil {
		out.View.Progresses = make(map[string]domain.Progress, len(snap.View.Progresses))
		for k, v := range snap.View.Progresses {
			out.View.Progresses[k] = cloneProgress(v)
		}
	}
	return out
}

func cloneSummary(in domain.Summary) domain.Summary {
	out := in
	out.UserIntents = append([]string(nil), in.UserIntents...)
	out.DecisionsMade = append([]domain.Decision(nil), in.DecisionsMade...)
	out.OpenQuestions = append([]string(nil), in.OpenQuestions...)
	out.NextActions = append([]string(nil), in.NextActions...)
	if in.ToolResults != nil {
		out.ToolResults = make(map[string]any, len(in.ToolResults))
		for k, v := range in.ToolResults {
			out.ToolResults[k] = cloneAny(v)
		}
	}
	return out
}

func cloneProgress(in domain.Progress) domain.Progress {
	out := in
	out.Done = append([]domain.Step(nil), in.Done...)
	out.Next = append([]domain.Step(nil), in.Next...)
	if in.Open != nil {
		out.Open = make([]domain.OpenLoop, len(in.Open))
		for i, loop := range in.Open {
			out.Open[i] = loop
			out.Open[i].BlockingSteps = append([]int(nil), loop.BlockingSteps...)
		}
	}
	return out
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = cloneAny(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = cloneAny(item)
		}
		return out
	default:
		return v
	}
}
