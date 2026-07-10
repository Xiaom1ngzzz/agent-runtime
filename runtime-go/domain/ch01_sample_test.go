package domain

import "testing"

// TestCh01SampleReplay 证明第一章 §1.6 那份样本 Event 流能被折叠成一个
// 内部一致的 SessionView：Task 成功、共 3 个 Turn、token 汇总正确。
// 这是"事件优先"决策在最简形态下的可运行证据。
func TestCh01SampleReplay(t *testing.T) {
	events := Ch01Sample()

	// 因果链完整性：每个 CausedBy 都要能在此前的 Event 中找到。
	seen := map[string]bool{}
	for _, e := range events {
		if e.CausedBy != "" && !seen[e.CausedBy] {
			t.Fatalf("event %s references unknown CausedBy=%s", e.ID, e.CausedBy)
		}
		seen[e.ID] = true
	}

	view := FoldSample(events)

	if view.Session.ID != "s1" || view.Session.Principal != "user-42" {
		t.Fatalf("session view wrong: %+v", view.Session)
	}

	task, ok := view.Tasks["t1"]
	if !ok {
		t.Fatal("task t1 missing after replay")
	}
	if task.Status != TaskSucceeded {
		t.Fatalf("task status want succeeded, got %s", task.Status)
	}

	turn, ok := view.LastTurn["t1"]
	if !ok || turn.ID != "r3" || turn.Index != 2 {
		t.Fatalf("last turn should be r3 (index 2), got %+v", turn)
	}
	if turn.Status != TurnDone {
		t.Fatalf("last turn should be done, got %s", turn.Status)
	}

	// 三个 Turn 的 tokens_in 汇总应为 520 + 610 + 700 = 1830
	var totalIn int
	for _, e := range events {
		if p, ok := e.Payload.(PayloadTurnEnded); ok {
			totalIn += p.TokensIn
		}
	}
	if totalIn != 1830 {
		t.Fatalf("total tokens_in want 1830, got %d", totalIn)
	}
}
