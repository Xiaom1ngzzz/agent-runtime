package domain

// Summary 是一段 Turn 范围的结构化摘要。见 ch04 §4.6.1。
//
// 关键设计:
//   - 结构化 (schema)，不是自然语言 —— 可 diff / merge / selective pluck。
//   - 每条 Decision 带 AtSeq —— 回溯到原始 Event，不是"遗忘"，是"索引"。
//   - 带 ModelUsed / PromptVersion —— 摘要本身可追踪版本，出问题能定位。
type Summary struct {
	SessionID string
	TaskID    string
	FromSeq   int64
	ToSeq     int64

	UserIntents   []string       // 用户在这段内表达过的目标
	ToolResults   map[string]any // key = "tool_name:key_arg"，value = 关键返回值
	DecisionsMade []Decision     // Agent 已做的选择
	OpenQuestions []string       // 尚未回答的问题
	NextActions   []string       // 计划中的动作

	ModelUsed     string  // 生成本摘要用的模型
	PromptVersion string  // 生成本摘要用的 Prompt 版本
	Confidence    float64 // 生成器自评，0-1
}

// Decision 是一次 Agent 侧的显式选择。§4.6.1。
type Decision struct {
	What  string // "选择走 A 方案而不是 B"
	Why   string // "因为用户明确说 Alice 不吃辣"
	AtSeq int64  // 决策发生的 Event seq，用于回溯到原文
}

// Progress 是一个 Task 的进度快照。见 ch04 §4.7.2。
// Progress 不是百分比 —— 是一张有状态节点的图。
type Progress struct {
	Goal string     // Task.Goal 的拷贝
	Done []Step     // 已完成的关键 Step
	Next []Step     // 计划中的 Step
	Open []OpenLoop // 未闭合的子问题

	Version   int64  // 每次更新递增
	UpdatedAt string // 最近一次触发更新的 Event ID
}

// Step 是 Progress 里的一个语义节点。见 ch04 §4.7.2/§4.7.3。
type Step struct {
	Intent      string   // "查询北京明天天气"
	Action      string   // "call tool: weather"
	Observation string   // "temp=26 sky=多云"  (短事实，不是原文)
	Cost        float64  // token or dollar (可选)
	Duration    int64    // ms (可选)
	Kind        StepKind // decision | tool_call | user_input | ...
}

// StepKind 用于回收:read-only probe 可丢，有 side effect 必留。§4.7.3。
type StepKind string

const (
	StepDecision   StepKind = "decision"
	StepToolCall   StepKind = "tool_call"
	StepUserInput  StepKind = "user_input"
	StepError      StepKind = "error"
	StepReadOnly   StepKind = "read_only" // 可丢
	StepAggregated StepKind = "aggregated" // "批量查 20 个订单" 这类聚合节点
)

// OpenLoop 是一个尚未闭合的子问题。§4.7.2。
type OpenLoop struct {
	Question      string
	RaisedAt      string // Event ID
	BlockingSteps []int  // Step 下标；这些计划步骤依赖此问题被解答
}

// TurnDigest 是 WorkingSet 里的一条项目。见 ch04 §4.4.1。
// 它不是 Turn 全量，是"回来投影时够用的最小信息"。
type TurnDigest struct {
	TurnID     string
	TaskID     string
	Index      int
	FromSeq    int64  // 该 Turn 覆盖的 seq 起点
	ToSeq      int64  // 该 Turn 覆盖的 seq 终点
	Superseded bool   // 若已被 ContextCompressed 覆盖，Assemble 时跳过原文
}

// MemoryRef 是从 Memory 层查回来的相关片段。ch05 展开；ch04 只存字段。
type MemoryRef struct {
	Source   string // "vector:kb.docs" | "kv:facts" | ...
	Key      string
	Content  string
	Score    float64
	QueriedAtSeq int64
}
