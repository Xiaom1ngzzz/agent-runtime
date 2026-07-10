package memory_test

// TestCh05MemoryCycle 是 ch05 §5.10.2 承诺的端到端证据。
//
// 场景:
//   1. Batch 导入 3 条 Semantic Memory(用户偏好 + KB 文档)。
//   2. 归档 1 条 Episodic Memory(Session Summary)。
//   3. 新 Session 启动一个 Task,上层 Loop 调 MemoryStore.Query。
//   4. 把返回的 Refs 打包成 MemoryQueried Event,追加到 EventStore + Apply 到 State。
//   5. LayeredContextEngine.Assemble 拼出的 Context 中,应含 <memory_ref> 块。
//   6. 全量回放后 SessionView.MemoryRefs 一致。
//
// 另外一条独立断言:
//   - Upsert 幂等(同 Version 写两次 = 一次)
//   - Query 返回按 Score 降序
//   - Query 尊重 MinScore

import (
	stdctx "context"
	"strings"
	"testing"

	rtctx "agent-runtime-go/context"
	"agent-runtime-go/domain"
	"agent-runtime-go/memory"
	"agent-runtime-go/runtime/memfakes"
)

const embedDim = 64

func TestCh05MemoryCycle(t *testing.T) {
	ctx := stdctx.Background()
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }

	// ---------- 1. 搭建 MemoryStore + 导入种子数据 ----------
	mem := memory.NewInMemStore()

	must(mem.Upsert(ctx, domain.MemoryItem{
		ID: "m1", Source: "user_pref", Kind: domain.MemorySemantic,
		Key: "user:42:diet", Content: "不吃辣,偏好清淡",
		Embedding: memory.EmbedText("不吃辣", embedDim),
		Tags:      []string{"user:42"}, Version: 1,
	}))
	must(mem.Upsert(ctx, domain.MemoryItem{
		ID: "m2", Source: "user_pref", Kind: domain.MemorySemantic,
		Key: "user:42:email", Content: "xiaoming@example.com",
		Embedding: memory.EmbedText("邮箱 email", embedDim),
		Tags:      []string{"user:42"}, Version: 1,
	}))
	must(mem.Upsert(ctx, domain.MemoryItem{
		ID: "m3", Source: "kb.docs", Kind: domain.MemorySemantic,
		Key: "kb:travel:policy", Content: "公司差旅政策:经济舱,住宿限额 800",
		Embedding: memory.EmbedText("差旅政策 travel policy", embedDim),
		Tags:      []string{"domain:travel"}, Version: 1,
	}))
	must(mem.Upsert(ctx, domain.MemoryItem{
		ID: "m4", Source: "session_summary", Kind: domain.MemoryEpisodic,
		Key: "session:s0:t0", Content: "上次订了周三从北京到上海的机票,值得复用",
		Embedding:     memory.EmbedText("订机票 北京 上海", embedDim),
		Tags:          []string{"user:42", "domain:travel"},
		OriginSession: "s0", OriginTaskID: "t0", OriginSeqFrom: 1, OriginSeqTo: 40,
		Version: 1,
	}))

	// ---------- 断言 A: Upsert 幂等 ----------
	sameItem := domain.MemoryItem{
		ID: "m1", Source: "user_pref", Kind: domain.MemorySemantic,
		Key: "user:42:diet", Content: "不吃辣,偏好清淡",
		Embedding: memory.EmbedText("不吃辣", embedDim),
		Tags:      []string{"user:42"}, Version: 1,
	}
	must(mem.Upsert(ctx, sameItem)) // 同 Version:忽略,不报错
	// 再查一次 diet:仍然只有一条
	refs, err := mem.Query(ctx, domain.Query{
		Keywords: []string{"user:42:diet"}, TopK: 10, MinScore: 0,
	})
	must(err)
	dietCount := 0
	for _, r := range refs {
		if r.Key == "user:42:diet" {
			dietCount++
		}
	}
	if dietCount != 1 {
		t.Fatalf("expected exactly 1 diet ref after idempotent upsert, got %d", dietCount)
	}

	// ---------- 2. 组合查询(§5.5.4) ----------
	refs, err = mem.Query(ctx, domain.Query{
		Semantic:     "帮 alice 订机票",
		Tags:         []string{"user:42"},
		SourceFilter: []string{"session_summary", "user_pref"},
		TopK:         3,
		MinScore:     0.0,
	})
	must(err)
	if len(refs) == 0 {
		t.Fatal("expected memory refs from combined query")
	}

	// ---------- 断言 B: Score 降序 ----------
	for i := 1; i < len(refs); i++ {
		if refs[i].Score > refs[i-1].Score {
			t.Fatalf("refs not sorted by score desc: %v", refs)
		}
	}

	// ---------- 3. 追加 MemoryQueried Event(§5.7.2) ----------
	store := memfakes.NewEventStore()
	st := memfakes.NewState()
	const sid, tid, turnID = "s1", "t1", "r1"
	seed := []domain.Event{
		{SessionID: sid, Type: domain.EvtSessionOpened,
			Payload: domain.PayloadSessionOpened{Principal: "user-42"}},
		{SessionID: sid, TaskID: tid, Type: domain.EvtTaskCreated,
			Payload: domain.PayloadTaskCreated{Goal: "帮我订机票"}},
		{SessionID: sid, TaskID: tid, TurnID: turnID, Type: domain.EvtTurnStarted,
			Payload: domain.PayloadTurnStarted{Index: 0}},
		{SessionID: sid, TaskID: tid, TurnID: turnID, Type: domain.EvtMemoryQueried,
			Payload: domain.PayloadMemoryQueried{
				Query: "帮 alice 订机票",
				Refs:  refs,
			}},
	}
	must(store.Append(seed))
	must(st.Apply(seed))

	// ---------- 4. LayeredContextEngine.Assemble 拼 Context(应含 <memory_ref>) ----------
	layered := &rtctx.LayeredContextEngine{
		State:        st,
		Store:        store,
		Instructions: "You are an agent.",
	}
	c, err := layered.Assemble(ctx, sid, tid)
	must(err)
	found := false
	for _, m := range c.Messages {
		if strings.Contains(m.Content, "<memory_ref") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Assemble output should contain <memory_ref> block")
	}

	// ---------- 5. 回放性:全量 Fold 后 SessionView.MemoryRefs 一致 ----------
	fresh := memfakes.NewState()
	must(fresh.Apply(store.Snapshot()))
	view1, err := st.View(sid)
	must(err)
	view2, err := fresh.View(sid)
	must(err)
	if len(view1.MemoryRefs) != len(view2.MemoryRefs) {
		t.Fatalf("memory refs mismatch after replay: %d vs %d",
			len(view1.MemoryRefs), len(view2.MemoryRefs))
	}
	for i := range view1.MemoryRefs {
		if view1.MemoryRefs[i].Key != view2.MemoryRefs[i].Key {
			t.Fatalf("ref[%d] mismatch: %s vs %s",
				i, view1.MemoryRefs[i].Key, view2.MemoryRefs[i].Key)
		}
	}
}

// TestCh05MinScoreFilter 单独证明 §5.5.5 反例的正解:MinScore 严格过滤。
func TestCh05MinScoreFilter(t *testing.T) {
	ctx := stdctx.Background()
	mem := memory.NewInMemStore()
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }

	// 两条 embedding 差异很大的 item
	must(mem.Upsert(ctx, domain.MemoryItem{
		ID: "a", Key: "a", Content: "苹果 apple",
		Embedding: memory.EmbedText("苹果 apple", embedDim),
		Kind:      domain.MemorySemantic, Version: 1,
	}))
	must(mem.Upsert(ctx, domain.MemoryItem{
		ID: "b", Key: "b", Content: "订机票 book flight",
		Embedding: memory.EmbedText("订机票 book flight", embedDim),
		Kind:      domain.MemorySemantic, Version: 1,
	}))

	// 高阈值 → 只能匹配到相似的
	refs, err := mem.Query(ctx, domain.Query{
		Semantic: "订机票", TopK: 10, MinScore: 0.9,
	})
	must(err)
	for _, r := range refs {
		if r.Score < 0.9 {
			t.Fatalf("score %.3f below MinScore 0.9", r.Score)
		}
	}
	// 至少能查到 b (自匹配)
	foundB := false
	for _, r := range refs {
		if r.Key == "b" {
			foundB = true
		}
	}
	if !foundB {
		t.Fatal("expected 'b' in high-score results (self match)")
	}
}
