package domain

// EventPayload 是所有 Event Payload 的 marker 接口。
// 每个具体 payload 类型实现 eventPayload()，只是为了让 Event.Payload
// 在类型系统上收紧一层——避免任何东西都能塞进 Payload。
type EventPayload interface {
	eventPayload()
}

// ---------- Session ----------

type PayloadSessionOpened struct {
	Principal string
	Metadata  map[string]string
}

func (PayloadSessionOpened) eventPayload() {}

// ---------- Task ----------

type PayloadTaskCreated struct {
	Goal   string
	Budget Budget
}

func (PayloadTaskCreated) eventPayload() {}

type PayloadTaskEnded struct {
	Status TaskStatus
	Reason string // 可选：canceled/timeout/failed 时说明原因
}

func (PayloadTaskEnded) eventPayload() {}

// ---------- Turn ----------

type PayloadTurnStarted struct {
	Index int
}

func (PayloadTurnStarted) eventPayload() {}

type PayloadTurnEnded struct {
	Status    TurnStatus
	TokensIn  int
	TokensOut int
	CostUS    float64
	LatencyMS int64
}

func (PayloadTurnEnded) eventPayload() {}

// ---------- 用户输入 ----------

type PayloadUserSpoke struct {
	Text string
}

func (PayloadUserSpoke) eventPayload() {}

// ---------- LLM 交互 ----------

type PayloadLLMRequested struct {
	Model    string
	Messages []Message // 组装好的最终上下文
	Tools    []Tool
}

func (PayloadLLMRequested) eventPayload() {}

type PayloadLLMReplied struct {
	Assistant Message
	ToolCalls []ToolCall
	TokensIn  int
	TokensOut int
}

func (PayloadLLMReplied) eventPayload() {}

// ---------- 工具调用 ----------

type PayloadToolCalled struct {
	CallID    string
	Name      string
	Arguments string // JSON 字符串,与 ToolCall.Arguments 一致
}

func (PayloadToolCalled) eventPayload() {}

type PayloadToolReturned struct {
	CallID  string
	Content string // 序列化后的返回值
	IsError bool
}

func (PayloadToolReturned) eventPayload() {}

// ---------- 上下文压缩 (ch04) ----------

// PayloadContextCompressed 说明"哪一段 Event 被压缩成什么摘要"。见 ch04 §4.5.3。
// 是回放性的核心保证:摘要作为不可变事实进 EventStore。
type PayloadContextCompressed struct {
	FromSeq    int64   // 覆盖的 seq 范围起点
	ToSeq      int64   // 覆盖的 seq 范围终点
	Strategy   string  // "turns:8" | "task-end" | "manual" | "fallback:flat" | ...
	Summary    Summary // 结构化摘要,见 §4.6.1
	FromTokens int     // 压缩前估算 token(可选)
	ToTokens   int     // 摘要后 token(可选)
}

func (PayloadContextCompressed) eventPayload() {}

// PayloadCompressionSkipped 记录"这次压缩尝试失败/跳过"。见 ch04 §4.9。
type PayloadCompressionSkipped struct {
	Reason string // "llm_error" | "schema_invalid" | ...
	Detail string
}

func (PayloadCompressionSkipped) eventPayload() {}

// ---------- 任务进度 (ch04 §4.7) ----------

// PayloadProgressUpdated 记录一次 Progress 折叠。幂等:同 Version 写两次结果一样。
type PayloadProgressUpdated struct {
	TaskID   string
	Progress Progress
}

func (PayloadProgressUpdated) eventPayload() {}

// ---------- Memory (ch05 会展开) ----------

// PayloadMemoryQueried 记录一次向量库/KV 检索的结果。ch04 只定义,ch05 用。
type PayloadMemoryQueried struct {
	Query string
	Refs  []MemoryRef
}

func (PayloadMemoryQueried) eventPayload() {}
