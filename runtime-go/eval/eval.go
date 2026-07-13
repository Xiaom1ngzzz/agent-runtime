// Package eval 提供最小评测框架(ch10)。
//
// 输入:一份"金标" Event 流 + 实际跑出来的 Event 流 / SessionView。
// 输出:结构化 Score(事件数、终态、token、工具错误率)。
package eval

import (
	"encoding/json"
	"fmt"

	"agent-runtime-go/domain"
)

// Metrics 是单条事件流归一化后的可比较指标。
type Metrics struct {
	EventCount int
	TokensIn   int
	TokensOut  int
	ToolCalls  int
	ToolErrors int
}

// Score 是一次评测的结构化结果。
type Score struct {
	EventCountMatch    bool
	EventSequenceMatch bool
	FinalTaskStatus    domain.TaskStatus
	FoundTerminal      bool
	StatusMatch        bool
	Golden             Metrics
	Actual             Metrics
	TokenDeltaIn       int
	TokenDeltaOut      int
	TokensIn           int // 兼容字段:Actual.TokensIn
	TokensOut          int // 兼容字段:Actual.TokensOut
	ToolCalls          int // 兼容字段:Actual.ToolCalls
	ToolErrors         int // 兼容字段:Actual.ToolErrors
	ToolErrorRate      float64
	Passed             bool
	Notes              []string
}

type streamSummary struct {
	metrics       Metrics
	fingerprints  []string
	finalStatus   domain.TaskStatus
	foundTerminal bool
}

// CompareStreams 对比金标与实际事件流的协议结构和关键 payload,忽略时间戳与 Event ID。
func CompareStreams(golden, actual []domain.Event, taskID string) Score {
	g := summarize(filterTask(golden, taskID))
	a := summarize(filterTask(actual, taskID))
	s := Score{
		EventCountMatch:    g.metrics.EventCount == a.metrics.EventCount,
		EventSequenceMatch: equalStrings(g.fingerprints, a.fingerprints),
		FinalTaskStatus:    a.finalStatus,
		FoundTerminal:      a.foundTerminal,
		StatusMatch:        g.foundTerminal && a.foundTerminal && g.finalStatus == a.finalStatus,
		Golden:             g.metrics,
		Actual:             a.metrics,
		TokenDeltaIn:       a.metrics.TokensIn - g.metrics.TokensIn,
		TokenDeltaOut:      a.metrics.TokensOut - g.metrics.TokensOut,
		TokensIn:           a.metrics.TokensIn,
		TokensOut:          a.metrics.TokensOut,
		ToolCalls:          a.metrics.ToolCalls,
		ToolErrors:         a.metrics.ToolErrors,
	}
	if !s.EventCountMatch {
		s.Notes = append(s.Notes, "event count mismatch")
	}
	if !s.EventSequenceMatch {
		s.Notes = append(s.Notes, "event sequence or key payload mismatch")
	}
	if !g.foundTerminal {
		s.Notes = append(s.Notes, "golden terminal task status missing")
	}
	if !a.foundTerminal {
		s.Notes = append(s.Notes, "actual terminal task status missing")
	}
	if s.Actual.ToolCalls > 0 {
		s.ToolErrorRate = float64(s.Actual.ToolErrors) / float64(s.Actual.ToolCalls)
	} else if s.Actual.ToolErrors > 0 {
		s.ToolErrorRate = 1
	}
	if !s.StatusMatch {
		s.Notes = append(s.Notes, "final task status mismatch")
	}
	if s.Golden.ToolCalls != s.Actual.ToolCalls {
		s.Notes = append(s.Notes, "tool call count mismatch")
	}
	if s.Golden.ToolErrors != s.Actual.ToolErrors {
		s.Notes = append(s.Notes, "tool error count mismatch")
	}
	s.Passed = s.EventCountMatch &&
		s.EventSequenceMatch &&
		s.StatusMatch &&
		s.Golden.ToolCalls == s.Actual.ToolCalls &&
		s.Golden.ToolErrors == s.Actual.ToolErrors
	return s
}

func filterTask(events []domain.Event, taskID string) []domain.Event {
	if taskID == "" {
		return append([]domain.Event(nil), events...)
	}
	out := make([]domain.Event, 0, len(events))
	for _, e := range events {
		if e.TaskID == taskID {
			out = append(out, e)
		}
	}
	return out
}

func summarize(events []domain.Event) streamSummary {
	out := streamSummary{metrics: Metrics{EventCount: len(events)}}
	callOrder := map[string]int{}
	failedCalls := map[string]bool{}
	nextCall := 0
	for i, e := range events {
		out.fingerprints = append(out.fingerprints, eventFingerprint(e, callOrder, &nextCall))
		switch p := e.Payload.(type) {
		case domain.PayloadTaskEnded:
			out.finalStatus, out.foundTerminal = p.Status, true
		case domain.PayloadTurnEnded:
			out.metrics.TokensIn += p.TokensIn
			out.metrics.TokensOut += p.TokensOut
		case domain.PayloadToolCalled:
			out.metrics.ToolCalls++
			if _, ok := callOrder[p.CallID]; !ok {
				callOrder[p.CallID] = nextCall
				nextCall++
			}
		case domain.PayloadToolReturned:
			if p.IsError {
				failedCalls[errorKey(p.CallID, i)] = true
			}
		case domain.PayloadToolBindFailed:
			failedCalls[errorKey(p.CallID, i)] = true
		}
	}
	out.metrics.ToolErrors = len(failedCalls)
	return out
}

func eventFingerprint(e domain.Event, callOrder map[string]int, nextCall *int) string {
	switch p := e.Payload.(type) {
	case domain.PayloadTaskCreated:
		return fmt.Sprintf("%s|goal=%s|parent=%s", e.Type, p.Goal, p.ParentID)
	case domain.PayloadTaskEnded:
		return fmt.Sprintf("%s|status=%s", e.Type, p.Status)
	case domain.PayloadUserSpoke:
		return fmt.Sprintf("%s|text=%s", e.Type, p.Text)
	case domain.PayloadLLMReplied:
		return fmt.Sprintf("%s|tools=%d|tokens=%d/%d", e.Type, len(p.ToolCalls), p.TokensIn, p.TokensOut)
	case domain.PayloadLLMRequested:
		return fmt.Sprintf("%s|model=%s|msgs=%d", e.Type, p.Model, len(p.Messages))
	case domain.PayloadTurnEnded:
		return fmt.Sprintf("%s|status=%s", e.Type, p.Status)
	case domain.PayloadToolCalled:
		idx := canonicalCall(p.CallID, callOrder, nextCall)
		return fmt.Sprintf("%s|call=%d|name=%s|args=%s", e.Type, idx, p.Name, normalizeJSON(p.Arguments))
	case domain.PayloadToolReturned:
		idx := canonicalCall(p.CallID, callOrder, nextCall)
		return fmt.Sprintf("%s|call=%d|error=%t", e.Type, idx, p.IsError)
	case domain.PayloadToolBindFailed:
		idx := canonicalCall(p.CallID, callOrder, nextCall)
		return fmt.Sprintf("%s|call=%d|name=%s|reason=%s", e.Type, idx, p.Name, p.Reason)
	default:
		return string(e.Type)
	}
}

func canonicalCall(id string, order map[string]int, next *int) int {
	if idx, ok := order[id]; ok {
		return idx
	}
	idx := *next
	order[id] = idx
	*next++
	return idx
}

func normalizeJSON(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	data, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(data)
}

func errorKey(callID string, index int) string {
	if callID != "" {
		return callID
	}
	return fmt.Sprintf("orphan:%d", index)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ScoreView 对 SessionView 做轻量检查(任务终态 + Progress 版本)。
func ScoreView(view domain.SessionView, taskID string, wantStatus domain.TaskStatus, minProgressVer int64) Score {
	task, taskOK := view.Tasks[taskID]
	prog, progressOK := view.Progresses[taskID]
	s := Score{FinalTaskStatus: task.Status, FoundTerminal: taskOK && isTerminal(task.Status)}
	s.StatusMatch = s.FoundTerminal && task.Status == wantStatus
	if !taskOK {
		s.Notes = append(s.Notes, "task missing")
	} else if !s.FoundTerminal {
		s.Notes = append(s.Notes, "task is not terminal")
	}
	if !progressOK {
		s.Notes = append(s.Notes, "progress missing")
	}
	if prog.Version < minProgressVer {
		s.Notes = append(s.Notes, "progress version too low")
	}
	s.Passed = s.StatusMatch && progressOK && prog.Version >= minProgressVer
	return s
}

func isTerminal(status domain.TaskStatus) bool {
	switch status {
	case domain.TaskSucceeded, domain.TaskFailed, domain.TaskCanceled, domain.TaskTimeout:
		return true
	default:
		return false
	}
}
