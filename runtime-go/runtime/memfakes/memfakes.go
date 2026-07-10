// Package memfakes 提供第 2 章端到端 demo 用的最小依赖实现。
// 它们不是生产实现 —— 生产版本在后续章节各自展开。
// 唯一目的是让 §2.4 的 Runtime 协调器能在没有真 LLM/Tool 的情况下跑通数据流。
package memfakes

import (
	stdctx "context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	rtctx "agent-runtime-go/context"
	"agent-runtime-go/domain"
	"agent-runtime-go/executor"
	"agent-runtime-go/llm"
	"agent-runtime-go/prompt"
)

// ---------- EventStore ----------

// EventStore 是最小可行的内存实现：全局 mutex 兜底，每 session 独立 seq 计数。
// ch03 §3.4.3 L1 档次;生产实现见 ch09 讨论。
type EventStore struct {
	mu     sync.Mutex
	events []domain.Event
	nextID int
	seqBy  map[string]int64 // sessionID -> next seq;从 1 开始
}

func NewEventStore() *EventStore { return &EventStore{seqBy: map[string]int64{}} }

// Append 按 ch03 §3.4.2 契约:同 session 内 seq 严格单调递增,由 store 分配;
// id 若为空由 store 分配为 "eNN"。Turn 内的多条 event 会拿到连续 seq。
func (s *EventStore) Append(events []domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range events {
		if events[i].ID == "" {
			s.nextID++
			events[i].ID = "e" + pad(s.nextID, 2)
		}
		if events[i].Seq == 0 {
			s.seqBy[events[i].SessionID]++
			events[i].Seq = s.seqBy[events[i].SessionID]
		} else if events[i].Seq > s.seqBy[events[i].SessionID] {
			// 允许客户端预填(如 ch01 sample 走另一条路),但要把计数器推到最新。
			s.seqBy[events[i].SessionID] = events[i].Seq
		}
		if events[i].TS.IsZero() {
			events[i].TS = time.Now().UTC()
		}
		s.events = append(s.events, events[i])
	}
	return nil
}

func (s *EventStore) Load(sessionID string) ([]domain.Event, error) {
	return s.LoadFrom(sessionID, 0)
}

// LoadFrom 返回 seq > fromSeq 的所有事件,按 seq 升序。fromSeq=0 = 全量。
func (s *EventStore) LoadFrom(sessionID string, fromSeq int64) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Event, 0, len(s.events))
	for _, e := range s.events {
		if e.SessionID != sessionID {
			continue
		}
		if e.Seq <= fromSeq {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *EventStore) Snapshot() []domain.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Event(nil), s.events...)
}

func pad(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// ---------- State ----------

// State 是内存里的 Fold 视图,与 EventStore 共享事件流。
type State struct {
	mu    sync.Mutex
	views map[string]*domain.SessionView
}

func NewState() *State { return &State{views: map[string]*domain.SessionView{}} }

// Apply 折入 Event。按 ch03 §3.5.4 校验:seq 严格递增、caused_by 已见、session_id 匹配。
// 违反不变量 = 事件流已损坏,立刻拒绝(§3.8)。
func (s *State) Apply(events []domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		v := s.views[e.SessionID]
		if v == nil {
			v = &domain.SessionView{
				Tasks:    map[string]domain.Task{},
				LastTurn: map[string]domain.Turn{},
				SeenIDs:  map[string]bool{},
			}
			s.views[e.SessionID] = v
		}
		if err := checkInvariants(v, e); err != nil {
			return err
		}
		applyOne(v, e)
		if e.Seq > v.MaxSeq {
			v.MaxSeq = e.Seq
		}
		if v.SeenIDs == nil {
			v.SeenIDs = map[string]bool{}
		}
		if e.ID != "" {
			v.SeenIDs[e.ID] = true
		}
	}
	return nil
}

// checkInvariants 只在数据可校验时启用:seq 只在 > 0 时才做单调校验(ch01 手工样本 seq=0 时跳过);
// caused_by 只在 SeenIDs 已初始化且非空时校验,避免打扰"手工构造的 fold demo"。
func checkInvariants(v *domain.SessionView, e domain.Event) error {
	if e.SessionID == "" {
		return errors.New("event.session_id is empty")
	}
	if e.Seq > 0 && e.Seq <= v.MaxSeq {
		return fmt.Errorf("event seq %d not strictly greater than view maxSeq %d (id=%s)",
			e.Seq, v.MaxSeq, e.ID)
	}
	if e.CausedBy != "" && len(v.SeenIDs) > 0 && !v.SeenIDs[e.CausedBy] {
		return fmt.Errorf("event %s references unknown caused_by=%s", e.ID, e.CausedBy)
	}
	return nil
}

func (s *State) View(sessionID string) (domain.SessionView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.views[sessionID]
	if v == nil {
		return domain.SessionView{}, fmt.Errorf("no view for session %s", sessionID)
	}
	return *v, nil
}

// LoadSnapshot 把一份已折叠的 View 作为初始状态注入,用于 §3.6.3 的恢复流程。
// 与 State 层其他 API 的关系:Apply 之后传进来的 events 应该是快照 seq 之后的增量。
func (s *State) LoadSnapshot(sessionID string, view domain.SessionView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if view.Tasks == nil {
		view.Tasks = map[string]domain.Task{}
	}
	if view.LastTurn == nil {
		view.LastTurn = map[string]domain.Turn{}
	}
	if view.SeenIDs == nil {
		view.SeenIDs = map[string]bool{}
	}
	s.views[sessionID] = &view
}

func applyOne(v *domain.SessionView, e domain.Event) {
	if v.Summaries == nil {
		v.Summaries = map[int64]domain.Summary{}
	}
	if v.Progresses == nil {
		v.Progresses = map[string]domain.Progress{}
	}
	switch p := e.Payload.(type) {
	case domain.PayloadSessionOpened:
		v.Session = domain.Session{ID: e.SessionID, Principal: p.Principal, CreatedAt: e.TS, LastActiveAt: e.TS}
	case domain.PayloadTaskCreated:
		v.Tasks[e.TaskID] = domain.Task{
			ID: e.TaskID, SessionID: e.SessionID, Goal: p.Goal,
			Status: domain.TaskRunning, Budget: p.Budget, StartedAt: e.TS,
		}
	case domain.PayloadTaskEnded:
		t := v.Tasks[e.TaskID]
		t.Status = p.Status
		t.EndedAt = e.TS
		v.Tasks[e.TaskID] = t
	case domain.PayloadTurnStarted:
		v.LastTurn[e.TaskID] = domain.Turn{ID: e.TurnID, TaskID: e.TaskID, Index: p.Index, Status: domain.TurnRunning}
	case domain.PayloadTurnEnded:
		t := v.LastTurn[e.TaskID]
		t.Status = p.Status
		t.TokensIn = p.TokensIn
		t.TokensOut = p.TokensOut
		t.CostUS = p.CostUS
		t.LatencyMS = p.LatencyMS
		v.LastTurn[e.TaskID] = t
		// ch04: 追加 TurnDigest 到 WorkingSet(§4.4.1)。
		v.WorkingSet = append(v.WorkingSet, domain.TurnDigest{
			TurnID: e.TurnID, TaskID: e.TaskID, Index: t.Index,
			// FromSeq/ToSeq 由更完善的实现填,这里用 e.Seq 兜底
			FromSeq: e.Seq, ToSeq: e.Seq,
		})
	case domain.PayloadContextCompressed:
		// ch04 §4.5.3: 存 Summary + mark 覆盖的 TurnDigest 为 Superseded。
		v.Summaries[p.FromSeq] = p.Summary
		for i := range v.WorkingSet {
			d := &v.WorkingSet[i]
			if d.ToSeq >= p.FromSeq && d.FromSeq <= p.ToSeq {
				d.Superseded = true
			}
		}
	case domain.PayloadProgressUpdated:
		// ch04 §4.7: 幂等替换。
		v.Progresses[p.TaskID] = p.Progress
	case domain.PayloadMemoryQueried:
		// ch05 会展开;这里只做 append。
		v.MemoryRefs = append(v.MemoryRefs, p.Refs...)
	}
	if !e.TS.IsZero() {
		v.Session.LastActiveAt = e.TS
	}
}

// ---------- ContextEngine ----------

// ContextEngine 把 SessionView 投影成 Context。
// ch02 极简策略:先读 Fold 后的 SessionView(生命周期),再只读 EventStore 展开消息原文。
// ch04 LayeredContextEngine 用 WorkingSet 做有界展开;这里为 demo 平铺全量消息。
type ContextEngine struct {
	State *State
	Store *EventStore
	Tools []domain.Tool
}

func NewContextEngine(state *State, store *EventStore, tools []domain.Tool) *ContextEngine {
	return &ContextEngine{State: state, Store: store, Tools: tools}
}

var _ rtctx.ContextEngine = (*ContextEngine)(nil)

func (c *ContextEngine) Assemble(_ stdctx.Context, sessionID, taskID string) (domain.Context, error) {
	view, err := c.State.View(sessionID)
	if err != nil {
		return domain.Context{}, fmt.Errorf("state.View: %w", err)
	}
	if taskID != "" {
		if _, ok := view.Tasks[taskID]; !ok {
			return domain.Context{}, fmt.Errorf("task %s not in SessionView", taskID)
		}
	}
	events, err := c.Store.Load(sessionID)
	if err != nil {
		return domain.Context{}, err
	}
	msgs := []domain.Message{{Role: "system", Content: "you are an agent."}}
	for _, e := range events {
		if e.TaskID != "" && e.TaskID != taskID {
			continue
		}
		switch p := e.Payload.(type) {
		case domain.PayloadUserSpoke:
			msgs = append(msgs, domain.Message{Role: "user", Content: p.Text})
		case domain.PayloadLLMReplied:
			m := p.Assistant
			if m.Role == "" {
				m.Role = "assistant"
			}
			m.ToolCalls = p.ToolCalls
			msgs = append(msgs, m)
		case domain.PayloadToolReturned:
			msgs = append(msgs, domain.Message{Role: "tool", ToolCallID: p.CallID, Content: p.Content})
		}
	}
	return domain.Context{SessionID: sessionID, TaskID: taskID, Messages: msgs, Tools: c.Tools}, nil
}

// ---------- PromptCompiler ----------

// PromptCompiler:占位实现,直接把 Context.Messages 作为最终 Messages。
type PromptCompiler struct{}

var _ prompt.PromptCompiler = (*PromptCompiler)(nil)

func (PromptCompiler) Compile(c domain.Context) (prompt.Messages, error) {
	return prompt.Messages(c.Messages), nil
}

// ---------- LLMProvider ----------

// LLMProvider 是脚本化的假模型 —— 按预设剧本回答,便于端到端复现。
type LLMProvider struct {
	Script []domain.LLMResponse
	idx    int
}

func NewLLMProvider(script []domain.LLMResponse) *LLMProvider {
	return &LLMProvider{Script: script}
}

var _ llm.LLMProvider = (*LLMProvider)(nil)

func (m *LLMProvider) Chat(_ stdctx.Context, _ prompt.Messages, _ []domain.Tool) (domain.LLMResponse, error) {
	if m.idx >= len(m.Script) {
		return domain.LLMResponse{}, errors.New("llm script exhausted")
	}
	resp := m.Script[m.idx]
	m.idx++
	return resp, nil
}

// ---------- Executor ----------

// Executor:根据 EventStore 里最新一条 LLMReplied 的 ToolCalls,查表调用 ToolFunc,追加 ToolCalled/ToolReturned Event。
type Executor struct {
	Store *EventStore
	Tools map[string]ToolFunc
}

type ToolFunc func(argsJSON string) (string, error)

func NewExecutor(store *EventStore, tools map[string]ToolFunc) *Executor {
	return &Executor{Store: store, Tools: tools}
}

var _ executor.Executor = (*Executor)(nil)

func (x *Executor) Run(_ stdctx.Context, turn domain.Turn) ([]domain.Event, error) {
	all := x.Store.Snapshot()
	var lastReplied *domain.PayloadLLMReplied
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].TurnID != turn.ID {
			continue
		}
		if p, ok := all[i].Payload.(domain.PayloadLLMReplied); ok {
			lastReplied = &p
			break
		}
	}
	if lastReplied == nil {
		return nil, errors.New("no LLMReplied in current turn")
	}
	out := make([]domain.Event, 0, 2*len(lastReplied.ToolCalls))
	for _, call := range lastReplied.ToolCalls {
		out = append(out, domain.Event{
			Type: domain.EvtToolCalled,
			Payload: domain.PayloadToolCalled{
				CallID: call.ID, Name: call.Name, Arguments: call.Arguments,
			},
		})
		fn, ok := x.Tools[call.Name]
		if !ok {
			out = append(out, domain.Event{
				Type: domain.EvtToolReturned,
				Payload: domain.PayloadToolReturned{
					CallID: call.ID, IsError: true, Content: "unknown tool: " + call.Name,
				},
			})
			continue
		}
		content, err := fn(call.Arguments)
		if err != nil {
			out = append(out, domain.Event{
				Type: domain.EvtToolReturned,
				Payload: domain.PayloadToolReturned{CallID: call.ID, IsError: true, Content: err.Error()},
			})
			continue
		}
		out = append(out, domain.Event{
			Type: domain.EvtToolReturned,
			Payload: domain.PayloadToolReturned{CallID: call.ID, Content: content},
		})
	}
	return out, nil
}
