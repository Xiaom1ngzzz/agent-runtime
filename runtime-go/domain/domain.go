// Package domain 定义 Runtime 世界里的四层核心对象与共享类型。
// 只包含数据结构，不含行为——行为在各操作包（context/prompt/llm/executor/state）中。
package domain

import "time"

// ---------- 三层聚合 ----------

// Session 是用户与系统的一段会话周期。
// 参见 chapters/ch01-runtime-domain.md §1.3。
type Session struct {
	ID           string
	Principal    string // 谁的 session：user id / api key / 服务账号
	CreatedAt    time.Time
	LastActiveAt time.Time
	Metadata     map[string]string
}

// Task 是 Session 内一件具体的事情。
// 是取消/重试/超时/成败评估的自然单位。
type Task struct {
	ID        string
	SessionID string
	Goal      string
	Status    TaskStatus
	Budget    Budget
	StartedAt time.Time
	EndedAt   time.Time // 零值表示未结束
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
	TaskTimeout   TaskStatus = "timeout"
)

// Budget 定义 Task 允许消耗的资源上限。
type Budget struct {
	MaxTokens int
	MaxCostUS float64
	MaxWallMS int64
}

// Turn 是 Runtime 与 LLM 的一次完整往返，
// 覆盖 LLM 调用 + 由此触发的所有工具调用 + 状态更新。
type Turn struct {
	ID        string
	TaskID    string
	Index     int
	Status    TurnStatus
	TokensIn  int
	TokensOut int
	CostUS    float64
	LatencyMS int64
}

type TurnStatus string

const (
	TurnRunning TurnStatus = "running"
	TurnDone    TurnStatus = "done"
	TurnFailed  TurnStatus = "failed"
)

// ---------- 原子事实 ----------

// Event 是 Runtime 中一次原子的状态变化的不可变记录。
// 追加式；State 是 Event 流的折叠结果。
type Event struct {
	ID        string
	SessionID string
	TaskID    string
	TurnID    string
	Type      EventType
	Payload   EventPayload // 与 Type 匹配；具体类型见 event_payloads.go
	TS        time.Time
	CausedBy  string // 上游 Event id，构成因果链
	Seq       int64  // 每 session 单调递增。由 EventStore 在 Append 时分配。0 表示尚未分配。
}

type EventType string

const (
	EvtSessionOpened     EventType = "SessionOpened"
	EvtTaskCreated       EventType = "TaskCreated"
	EvtTaskEnded         EventType = "TaskEnded"
	EvtTurnStarted       EventType = "TurnStarted"
	EvtTurnEnded         EventType = "TurnEnded"
	EvtUserSpoke         EventType = "UserSpoke"
	EvtLLMRequested      EventType = "LLMRequested"
	EvtLLMReplied        EventType = "LLMReplied"
	EvtToolCalled        EventType = "ToolCalled"
	EvtToolReturned      EventType = "ToolReturned"
	EvtContextCompressed EventType = "ContextCompressed"
)

// ---------- LLM 交互类型 ----------

type Message struct {
	Role       string     // "system" | "user" | "assistant" | "tool"
	Content    string     // 文本
	ToolCalls  []ToolCall // 仅当 Role == "assistant" 且模型请求调用工具
	ToolCallID string     // 仅当 Role == "tool"，关联到某次 ToolCall
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON 字符串
}

type ToolResult struct {
	CallID  string
	Content string // 序列化后的返回值
	IsError bool
}

type LLMResponse struct {
	Assistant Message
	ToolCalls []ToolCall // 便于遍历；同时冗余在 Assistant.ToolCalls 中
	TokensIn  int
	TokensOut int
}

// ---------- 工具描述 ----------

// Tool 是提供给 LLM 的能力描述（不含实现）。
// 具体调用由 Executor 通过 Tool.Name 分发到 Tool Runtime。
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema for arguments
}

// ---------- 上下文与视图 ----------

// Context 是 ContextEngine 组装出的、准备喂给 PromptCompiler 的中间物。
type Context struct {
	SessionID string
	TaskID    string
	TurnID    string
	Messages  []Message
	Tools     []Tool
}

// SessionView 是从 Event 流折叠出来的只读快照，供上下文与观测层使用。
type SessionView struct {
	Session  Session
	Tasks    map[string]Task
	LastTurn map[string]Turn // taskID -> latest Turn
	MaxSeq   int64           // 此 View 已折叠到的最大 seq；用于 §3.5.4 单调校验与 §3.6 Snapshot。
	SeenIDs  map[string]bool // 此 View 已见过的所有 Event.ID；用于 caused_by 因果链校验。
}
