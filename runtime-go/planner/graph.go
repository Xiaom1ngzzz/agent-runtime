package planner

import (
	stdcontext "context"
	"fmt"
	"strings"

	"agent-runtime-go/domain"
)

// GraphPlanner 是 ch07 Round 2 参考实现:
//   - 若父 Task Goal 含 " + " 分隔的子目标,且尚未派生子 Task,则产出 SubTaskSpawned + TaskCreated;
//   - 否则若 ShouldUpdate Progress,产出 ProgressUpdated。
//
// 预算继承:子 Task 均分父 Budget.MaxTokens(剩余字段原样拷贝)。
type GraphPlanner struct {
	// ChildIDFn 生成子 Task ID;测试可注入确定性 ID。
	ChildIDFn func(parentID string, index int) string
	Trigger   ProgressTrigger
}

func NewGraphPlanner() *GraphPlanner {
	return &GraphPlanner{
		ChildIDFn: defaultChildID,
		Trigger:   ToolCallTrigger{Every: 1},
	}
}

func defaultChildID(parentID string, index int) string {
	return fmt.Sprintf("%s.s%d", parentID, index+1)
}

// SplitGoals 把 "A + B + C" 拆成子目标列表;没有分隔符则返回空(不派生子 Task)。
func SplitGoals(goal string) []string {
	parts := strings.Split(goal, " + ")
	if len(parts) < 2 {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func (p *GraphPlanner) Plan(_ stdcontext.Context, view domain.SessionView, taskID string) ([]domain.Event, error) {
	task, ok := view.Tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("planner: unknown task %s", taskID)
	}
	g := domain.BuildTaskGraph(view.Tasks)
	children := g.ChildrenOf(taskID)

	goals := SplitGoals(task.Goal)
	if len(goals) >= 2 && len(children) == 0 {
		return p.spawnChildren(task, goals), nil
	}

	if p.Trigger != nil && p.Trigger.ShouldUpdate(view, taskID, nil) {
		prog := BuildProgressFromView(view, taskID)
		return []domain.Event{{
			Type:   domain.EvtProgressUpdated,
			TaskID: taskID,
			Payload: domain.PayloadProgressUpdated{
				TaskID:   taskID,
				Progress: prog,
			},
		}}, nil
	}
	return nil, nil
}

func (p *GraphPlanner) spawnChildren(parent domain.Task, goals []string) []domain.Event {
	n := len(goals)
	share := parent.Budget.MaxTokens / n
	if share < 1 && parent.Budget.MaxTokens > 0 {
		share = 1
	}
	out := make([]domain.Event, 0, n*2)
	for i, goal := range goals {
		childID := p.ChildIDFn(parent.ID, i)
		childBudget := parent.Budget
		childBudget.MaxTokens = share
		out = append(out,
			domain.Event{
				Type:   domain.EvtSubTaskSpawned,
				TaskID: parent.ID,
				Payload: domain.PayloadSubTaskSpawned{
					ParentTaskID: parent.ID,
					ChildTaskID:  childID,
					Goal:         goal,
					Budget:       childBudget,
				},
			},
			domain.Event{
				Type:   domain.EvtTaskCreated,
				TaskID: childID,
				Payload: domain.PayloadTaskCreated{
					Goal:     goal,
					Budget:   childBudget,
					ParentID: parent.ID,
				},
			},
		)
	}
	return out
}

// BuildProgressFromView 根据子 Task 状态与已有 Progress 拼一张图。
func BuildProgressFromView(view domain.SessionView, taskID string) domain.Progress {
	task := view.Tasks[taskID]
	prev := view.Progresses[taskID]
	g := domain.BuildTaskGraph(view.Tasks)
	children := g.ChildrenOf(taskID)

	var done, next []domain.Step
	for _, cid := range children {
		ct := view.Tasks[cid]
		step := domain.Step{
			Intent: ct.Goal,
			Action: "subtask:" + cid,
			Kind:   domain.StepDecision,
		}
		switch ct.Status {
		case domain.TaskSucceeded:
			step.Observation = "succeeded"
			done = append(done, step)
		case domain.TaskFailed, domain.TaskCanceled, domain.TaskTimeout:
			step.Observation = string(ct.Status)
			step.Kind = domain.StepError
			done = append(done, step)
		default:
			next = append(next, step)
		}
	}
	if len(children) == 0 && task.Goal != "" {
		next = append(next, domain.Step{
			Intent: task.Goal,
			Action: "execute",
			Kind:   domain.StepDecision,
		})
	}
	ver := prev.Version + 1
	if ver < 1 {
		ver = 1
	}
	return domain.Progress{
		Goal:      task.Goal,
		Done:      done,
		Next:      next,
		Open:      prev.Open,
		Version:   ver,
		UpdatedAt: "planner",
	}
}

// ToolCallTrigger:每累计 Every 次 ToolReturned 就建议更新 Progress。
// Round 2 简化:若 view 里尚无 Progress,或子图有变化,则触发。
type ToolCallTrigger struct {
	Every int
}

func (t ToolCallTrigger) ShouldUpdate(view domain.SessionView, taskID string, _ []domain.Event) bool {
	if _, ok := view.Progresses[taskID]; !ok {
		return true
	}
	g := domain.BuildTaskGraph(view.Tasks)
	if len(g.ChildrenOf(taskID)) > 0 {
		return true
	}
	return t.Every > 0
}

// SagaCoordinator 是 ADR-004 约定的最小 Process Manager:
// 父 Task 在所有子 Task 终态后,产出 TaskEnded。
type SagaCoordinator struct{}

func (SagaCoordinator) OnChildEnded(view domain.SessionView, parentID string) ([]domain.Event, error) {
	parent, ok := view.Tasks[parentID]
	if !ok {
		return nil, fmt.Errorf("saga: unknown parent %s", parentID)
	}
	if parent.Status != domain.TaskRunning && parent.Status != domain.TaskPending {
		return nil, nil
	}
	g := domain.BuildTaskGraph(view.Tasks)
	children := g.ChildrenOf(parentID)
	if len(children) == 0 {
		return nil, nil
	}
	allDone := true
	anyFailed := false
	for _, cid := range children {
		st := view.Tasks[cid].Status
		switch st {
		case domain.TaskSucceeded:
			// ok
		case domain.TaskFailed, domain.TaskCanceled, domain.TaskTimeout:
			anyFailed = true
		default:
			allDone = false
		}
	}
	if !allDone {
		return nil, nil
	}
	status := domain.TaskSucceeded
	reason := "all children succeeded"
	if anyFailed {
		status = domain.TaskFailed
		reason = "one or more children failed"
	}
	return []domain.Event{{
		Type:   domain.EvtTaskEnded,
		TaskID: parentID,
		Payload: domain.PayloadTaskEnded{Status: status, Reason: reason},
	}}, nil
}
