package domain

import "sort"

// TaskGraph 是 SessionView.Tasks 上的只读图视图(ch07)。
// 不单独持久化——由 Fold 出的 Tasks 派生。
type TaskGraph struct {
	Roots    []string            // ParentID == "" 的 Task ID
	Children map[string][]string // parentID -> child IDs(按 ID 稳定排序)
}

// BuildTaskGraph 从 SessionView.Tasks 派生图。
func BuildTaskGraph(tasks map[string]Task) TaskGraph {
	g := TaskGraph{
		Children: make(map[string][]string),
	}
	for id, t := range tasks {
		if t.ParentID == "" {
			g.Roots = append(g.Roots, id)
			continue
		}
		g.Children[t.ParentID] = append(g.Children[t.ParentID], id)
	}
	sort.Strings(g.Roots)
	for parentID := range g.Children {
		sort.Strings(g.Children[parentID])
	}
	return g
}

// ChildrenOf 返回直接子 Task ID 列表(可能为空)。
func (g TaskGraph) ChildrenOf(parentID string) []string {
	return g.Children[parentID]
}

// IsLeaf 判断 task 是否没有子节点。
func (g TaskGraph) IsLeaf(taskID string) bool {
	return len(g.Children[taskID]) == 0
}
