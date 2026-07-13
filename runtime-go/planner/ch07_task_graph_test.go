package planner_test

// TestCh07TaskGraphPlan 是 ch07 承诺的端到端证据:
//
//  1. 父 Goal "查天气 + 发邮件" → Plan 产出 2 个子 Task(SubTaskSpawned + TaskCreated);
//  2. Fold 后 Task.ParentID 正确,BuildTaskGraph 给出 roots/children;
//  3. 子 Task 全部 Succeeded 后 SagaCoordinator 关闭父 Task;
//  4. ProgressUpdated 反映 Done/Next。

import (
	stdctx "context"
	"testing"

	"agent-runtime-go/domain"
	"agent-runtime-go/planner"
	"agent-runtime-go/runtime/memfakes"
)

func TestCh07TaskGraphPlan(t *testing.T) {
	ctx := stdctx.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	store := memfakes.NewEventStore()
	st := memfakes.NewState()
	const sid, parentID = "s1", "t1"

	appendApply := func(evs ...domain.Event) {
		t.Helper()
		for _, e := range evs {
			e.SessionID = sid
			buf := []domain.Event{e}
			must(store.Append(buf))
			must(st.Apply(buf))
		}
	}

	appendApply(
		domain.Event{Type: domain.EvtSessionOpened, Payload: domain.PayloadSessionOpened{Principal: "u"}},
	)
	appendApply(
		domain.Event{
			Type:   domain.EvtTaskCreated,
			TaskID: parentID,
			Payload: domain.PayloadTaskCreated{
				Goal: "查天气 + 发邮件", Budget: domain.Budget{MaxTokens: 8000},
			},
		},
	)

	p := planner.NewGraphPlanner()
	view, err := st.View(sid)
	must(err)
	planned, err := p.Plan(ctx, view, parentID)
	must(err)
	if len(planned) != 4 { // 2 spawn + 2 create
		t.Fatalf("expected 4 plan events, got %d", len(planned))
	}
	appendApply(planned...)

	view, err = st.View(sid)
	must(err)
	g := domain.BuildTaskGraph(view.Tasks)
	if len(g.Roots) != 1 || g.Roots[0] != parentID {
		t.Fatalf("roots=%v", g.Roots)
	}
	children := g.ChildrenOf(parentID)
	if len(children) != 2 {
		t.Fatalf("children=%v", children)
	}
	for _, cid := range children {
		ct := view.Tasks[cid]
		if ct.ParentID != parentID {
			t.Fatalf("child %s parent=%q", cid, ct.ParentID)
		}
		if ct.Budget.MaxTokens != 4000 {
			t.Fatalf("child budget tokens=%d want 4000", ct.Budget.MaxTokens)
		}
	}

	// 子 Task 全部成功 → Saga 关父
	for _, cid := range children {
		appendApply(domain.Event{
			Type:   domain.EvtTaskEnded,
			TaskID: cid,
			Payload: domain.PayloadTaskEnded{Status: domain.TaskSucceeded},
		})
	}
	view, err = st.View(sid)
	must(err)
	saga := planner.SagaCoordinator{}
	ended, err := saga.OnChildEnded(view, parentID)
	must(err)
	if len(ended) != 1 {
		t.Fatalf("expected TaskEnded for parent, got %d", len(ended))
	}
	appendApply(ended...)
	view, err = st.View(sid)
	must(err)
	if view.Tasks[parentID].Status != domain.TaskSucceeded {
		t.Fatalf("parent status=%s", view.Tasks[parentID].Status)
	}

	// Progress
	progEvs, err := p.Plan(ctx, view, parentID)
	must(err)
	found := false
	for _, e := range progEvs {
		if e.Type == domain.EvtProgressUpdated {
			found = true
			pl := e.Payload.(domain.PayloadProgressUpdated)
			if len(pl.Progress.Done) != 2 {
				t.Fatalf("progress.done=%d", len(pl.Progress.Done))
			}
		}
	}
	if !found {
		t.Fatal("expected ProgressUpdated")
	}
}

func TestCh07SplitGoals(t *testing.T) {
	if got := planner.SplitGoals("单一目标"); got != nil {
		t.Fatalf("got %v", got)
	}
	got := planner.SplitGoals("查天气 + 发邮件")
	if len(got) != 2 || got[0] != "查天气" || got[1] != "发邮件" {
		t.Fatalf("got %v", got)
	}
}
