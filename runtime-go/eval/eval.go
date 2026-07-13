// Package eval 提供最小评测框架(ch10)。
//
// 输入:一份"金标" Event 流 + 实际跑出来的 Event 流 / SessionView。
// 输出:结构化 Score(事件数、终态、token、工具错误率)。
package eval

import (
	"agent-runtime-go/domain"
)

// Score 是一次评测的结构化结果。
type Score struct {
	EventCountMatch bool
	FinalTaskStatus domain.TaskStatus
	StatusMatch     bool
	TokensIn        int
	TokensOut       int
	ToolCalls       int
	ToolErrors      int
	ToolErrorRate   float64
	Passed          bool
	Notes           []string
}

// CompareStreams 对比金标与实际事件流的关键不变量(不是逐字节相等)。
func CompareStreams(golden, actual []domain.Event, taskID string) Score {
	s := Score{
		EventCountMatch: len(golden) == len(actual),
	}
	if !s.EventCountMatch {
		s.Notes = append(s.Notes, "event count mismatch")
	}

	var goldStatus, actStatus domain.TaskStatus
	for _, e := range golden {
		accumulate(&s, e, taskID, &goldStatus, true)
	}
	// reset counters for actual
	s.TokensIn, s.TokensOut, s.ToolCalls, s.ToolErrors = 0, 0, 0, 0
	for _, e := range actual {
		accumulate(&s, e, taskID, &actStatus, false)
	}
	s.FinalTaskStatus = actStatus
	s.StatusMatch = goldStatus == actStatus && goldStatus != ""
	if s.ToolCalls > 0 {
		s.ToolErrorRate = float64(s.ToolErrors) / float64(s.ToolCalls)
	}
	s.Passed = s.EventCountMatch && s.StatusMatch && s.ToolErrorRate == 0
	if !s.StatusMatch {
		s.Notes = append(s.Notes, "final task status mismatch")
	}
	return s
}

func accumulate(s *Score, e domain.Event, taskID string, status *domain.TaskStatus, _ bool) {
	if taskID != "" && e.TaskID != "" && e.TaskID != taskID {
		return
	}
	switch p := e.Payload.(type) {
	case domain.PayloadTaskEnded:
		*status = p.Status
	case domain.PayloadTurnEnded:
		s.TokensIn += p.TokensIn
		s.TokensOut += p.TokensOut
	case domain.PayloadToolCalled:
		s.ToolCalls++
	case domain.PayloadToolReturned:
		if p.IsError {
			s.ToolErrors++
		}
	case domain.PayloadToolBindFailed:
		s.ToolErrors++
	}
}

// ScoreView 对 SessionView 做轻量检查(任务终态 + Progress 版本)。
func ScoreView(view domain.SessionView, taskID string, wantStatus domain.TaskStatus, minProgressVer int64) Score {
	s := Score{FinalTaskStatus: view.Tasks[taskID].Status}
	s.StatusMatch = s.FinalTaskStatus == wantStatus
	prog := view.Progresses[taskID]
	if prog.Version < minProgressVer {
		s.Notes = append(s.Notes, "progress version too low")
	}
	s.Passed = s.StatusMatch && prog.Version >= minProgressVer
	return s
}
