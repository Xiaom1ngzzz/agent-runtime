package state_test

// TestCh09CheckpointRecover 是 ch09 承诺的端到端证据:
// 拍 Checkpoint → 新 State → Recover → View 与全量 Fold 一致,且只 replay 增量。

import (
	"testing"

	"agent-runtime-go/domain"
	"agent-runtime-go/runtime/memfakes"
	"agent-runtime-go/state"
)

func TestCh09CheckpointRecover(t *testing.T) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	store := memfakes.NewEventStore()
	st := memfakes.NewState()
	cps := state.NewMemCheckpointStore()
	const sid = "s1"

	appendApply := func(e domain.Event) {
		t.Helper()
		e.SessionID = sid
		buf := []domain.Event{e}
		must(store.Append(buf))
		must(st.Apply(buf))
	}

	appendApply(domain.Event{Type: domain.EvtSessionOpened, Payload: domain.PayloadSessionOpened{Principal: "u"}})
	appendApply(domain.Event{
		Type: domain.EvtTaskCreated, TaskID: "t1",
		Payload: domain.PayloadTaskCreated{Goal: "demo", Budget: domain.Budget{MaxTokens: 100}},
	})
	appendApply(domain.Event{
		Type: domain.EvtTurnStarted, TaskID: "t1", TurnID: "r1",
		Payload: domain.PayloadTurnStarted{Index: 0},
	})
	appendApply(domain.Event{
		Type: domain.EvtTurnEnded, TaskID: "t1", TurnID: "r1",
		Payload: domain.PayloadTurnEnded{Status: domain.TurnDone, TokensIn: 10, TokensOut: 2},
	})
	appendApply(domain.Event{
		Type: domain.EvtProgressUpdated, TaskID: "t1",
		Payload: domain.PayloadProgressUpdated{
			TaskID: "t1",
			Progress: domain.Progress{
				Goal: "demo", Version: 1,
				Done: []domain.Step{{Intent: "step1", Kind: domain.StepDecision}},
			},
		},
	})

	must(state.TakeCheckpoint(sid, st, cps))

	appendApply(domain.Event{
		Type: domain.EvtTaskEnded, TaskID: "t1",
		Payload: domain.PayloadTaskEnded{Status: domain.TaskSucceeded},
	})

	fresh := memfakes.NewState()
	n, err := state.Recover(sid, cps, store, fresh)
	must(err)
	if n != 1 {
		t.Fatalf("expected 1 replayed event, got %d", n)
	}

	got, err := fresh.View(sid)
	must(err)
	full := memfakes.NewState()
	must(full.Apply(store.Snapshot()))
	want, err := full.View(sid)
	must(err)

	if got.Tasks["t1"].Status != domain.TaskSucceeded {
		t.Fatalf("status=%s", got.Tasks["t1"].Status)
	}
	if got.Progresses["t1"].Version != want.Progresses["t1"].Version {
		t.Fatalf("progress not restored: got=%v want=%v", got.Progresses["t1"], want.Progresses["t1"])
	}
	if len(got.WorkingSet) != len(want.WorkingSet) {
		t.Fatalf("working_set len %d vs %d", len(got.WorkingSet), len(want.WorkingSet))
	}
}

func TestCh09CloneIncludesContextFields(t *testing.T) {
	store := state.NewMemSnapshotStore()
	view := domain.SessionView{
		Session:    domain.Session{ID: "s1"},
		Tasks:      map[string]domain.Task{"t1": {ID: "t1", Goal: "g"}},
		LastTurn:   map[string]domain.Turn{},
		SeenIDs:    map[string]bool{"e1": true},
		WorkingSet: []domain.TurnDigest{{TurnID: "r1", TaskID: "t1"}},
		Progresses: map[string]domain.Progress{"t1": {Goal: "g", Version: 2}},
		Summaries:  map[int64]domain.Summary{1: {FromSeq: 1, ToSeq: 2}},
		MemoryRefs: []domain.MemoryRef{{Key: "k", Content: "v"}},
		MaxSeq:     5,
	}
	if err := store.Save("s1", state.Snapshot{Seq: 5, View: view}); err != nil {
		t.Fatal(err)
	}
	view.Progresses["t1"] = domain.Progress{Version: 99}
	view.WorkingSet[0].TurnID = "mutated"

	snap, ok, err := store.Latest("s1")
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if snap.View.Progresses["t1"].Version != 2 {
		t.Fatalf("checkpoint polluted: version=%d", snap.View.Progresses["t1"].Version)
	}
	if snap.View.WorkingSet[0].TurnID != "r1" {
		t.Fatalf("working_set polluted: %s", snap.View.WorkingSet[0].TurnID)
	}
}
