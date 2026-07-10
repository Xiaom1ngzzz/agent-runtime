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

// ---------- 上下文压缩 ----------

type PayloadContextCompressed struct {
	FromTokens int
	ToTokens   int
	Strategy   string // 例如 "sliding-window" / "summarize"
}

func (PayloadContextCompressed) eventPayload() {}
