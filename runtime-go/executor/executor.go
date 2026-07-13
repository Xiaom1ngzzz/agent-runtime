// Package executor 驱动一个 Turn 的工具调用——读 LLMReplied、分发工具、产出 Event。
// 实现见 ch08-executor.md。与 memfakes.Executor 对齐并扩展:注册表、超时、ToolBindFailed。
package executor

import (
	stdcontext "context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"agent-runtime-go/domain"
	"agent-runtime-go/state"
)

// ToolFunc 是工具实现。argsJSON 为模型给出的 JSON 字符串。
type ToolFunc func(ctx stdcontext.Context, argsJSON string) (string, error)

// Registry 按名字绑定工具实现与描述。
type Registry struct {
	mu    sync.RWMutex
	funcs map[string]ToolFunc
	descs map[string]domain.Tool
}

func NewRegistry() *Registry {
	return &Registry{funcs: map[string]ToolFunc{}, descs: map[string]domain.Tool{}}
}

func (r *Registry) Register(desc domain.Tool, fn ToolFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.descs[desc.Name] = desc
	r.funcs[desc.Name] = fn
}

func (r *Registry) Lookup(name string) (ToolFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.funcs[name]
	return fn, ok
}

func (r *Registry) Descriptions() []domain.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Tool, 0, len(r.descs))
	for _, d := range r.descs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Executor 接口:驱动一个 Turn 完成工具侧 Emit。
type Executor interface {
	Run(ctx stdcontext.Context, turn domain.Turn) ([]domain.Event, error)
}

// ToolExecutor 是 ch08 Round 2 参考实现。
// 从 EventStore 读取当前 Turn 最新 LLMReplied,顺序(或并行)调用工具。
type ToolExecutor struct {
	Store     state.EventStore
	Registry  *Registry
	Timeout   time.Duration // 单工具超时;0 = 不额外限时(仍尊重 ctx)
	Parallel  bool          // true = 同 Turn 内工具并行
	Snapshots snapshotSource
}

// snapshotSource 让 ToolExecutor 既能吃 memfakes.EventStore,也能吃只暴露 Load 的 EventStore。
type snapshotSource interface {
	Snapshot() []domain.Event
}

func NewToolExecutor(store state.EventStore, reg *Registry) *ToolExecutor {
	return &ToolExecutor{Store: store, Registry: reg, Timeout: 5 * time.Second}
}

func (x *ToolExecutor) Run(ctx stdcontext.Context, turn domain.Turn) ([]domain.Event, error) {
	calls, err := x.loadToolCalls(turn.ID)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		return nil, nil
	}
	if x.Parallel && len(calls) > 1 {
		return x.runParallel(ctx, calls)
	}
	return x.runSequential(ctx, calls)
}

func (x *ToolExecutor) loadToolCalls(turnID string) ([]domain.ToolCall, error) {
	var all []domain.Event
	if x.Snapshots != nil {
		all = x.Snapshots.Snapshot()
	} else if s, ok := x.Store.(snapshotSource); ok {
		all = s.Snapshot()
	} else {
		// 无 Snapshot 时退化为全量 Load —— 调用方应保证 Store 可按 session 过滤;
		// Round 2 测试走 memfakes,总有 Snapshot。
		return nil, errors.New("executor: EventStore does not expose Snapshot; set ToolExecutor.Snapshots")
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].TurnID != turnID {
			continue
		}
		if p, ok := all[i].Payload.(domain.PayloadLLMReplied); ok {
			return p.ToolCalls, nil
		}
	}
	return nil, errors.New("no LLMReplied in current turn")
}

func (x *ToolExecutor) runSequential(ctx stdcontext.Context, calls []domain.ToolCall) ([]domain.Event, error) {
	out := make([]domain.Event, 0, 2*len(calls))
	for _, call := range calls {
		out = append(out, x.dispatchOne(ctx, call)...)
	}
	return out, nil
}

func (x *ToolExecutor) runParallel(ctx stdcontext.Context, calls []domain.ToolCall) ([]domain.Event, error) {
	type result struct {
		idx int
		evs []domain.Event
	}
	ch := make(chan result, len(calls))
	for i, call := range calls {
		i, call := i, call
		go func() {
			ch <- result{idx: i, evs: x.dispatchOne(ctx, call)}
		}()
	}
	buckets := make([][]domain.Event, len(calls))
	for range calls {
		r := <-ch
		buckets[r.idx] = r.evs
	}
	out := make([]domain.Event, 0, 2*len(calls))
	for _, evs := range buckets {
		out = append(out, evs...)
	}
	return out, nil
}

func (x *ToolExecutor) dispatchOne(ctx stdcontext.Context, call domain.ToolCall) []domain.Event {
	called := domain.Event{
		Type: domain.EvtToolCalled,
		Payload: domain.PayloadToolCalled{
			CallID: call.ID, Name: call.Name, Arguments: call.Arguments,
		},
	}
	fn, ok := x.Registry.Lookup(call.Name)
	if !ok {
		return []domain.Event{
			called,
			{
				Type: domain.EvtToolBindFailed,
				Payload: domain.PayloadToolBindFailed{
					CallID: call.ID, Name: call.Name, Reason: "unknown_tool",
				},
			},
			{
				Type: domain.EvtToolReturned,
				Payload: domain.PayloadToolReturned{
					CallID: call.ID, IsError: true, Content: "unknown tool: " + call.Name,
				},
			},
		}
	}
	callCtx := ctx
	cancel := func() {}
	if x.Timeout > 0 {
		callCtx, cancel = stdcontext.WithTimeout(ctx, x.Timeout)
	}
	defer cancel()

	content, err := fn(callCtx, call.Arguments)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, stdcontext.DeadlineExceeded) || callCtx.Err() == stdcontext.DeadlineExceeded {
			msg = fmt.Sprintf("tool timeout: %s", call.Name)
		}
		return []domain.Event{
			called,
			{
				Type:    domain.EvtToolReturned,
				Payload: domain.PayloadToolReturned{CallID: call.ID, IsError: true, Content: msg},
			},
		}
	}
	return []domain.Event{
		called,
		{
			Type:    domain.EvtToolReturned,
			Payload: domain.PayloadToolReturned{CallID: call.ID, Content: content},
		},
	}
}
