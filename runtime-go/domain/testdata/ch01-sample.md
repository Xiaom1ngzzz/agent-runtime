# ch01 · 事件流样本 · "查天气 + 发邮件"

对应第一章 §1.6。这份文件是 `TestCh01SampleReplay` 里的黄金样本，用 Go literal 而不是 JSON——因为 Event.Payload 走 marker interface，纯 JSON 反序列化需要一张 EventType→factory 表，那是第 3 章的话题。

按时间顺序：

| # | Type | 归属（session/task/turn） | 关键 payload |
|---|---|---|---|
| e01 | SessionOpened     | s1 / –  / –  | principal=user-42 |
| e02 | UserSpoke         | s1 / –  / –  | "帮我查一下明天北京的天气，然后写一封提醒邮件给 alice@example.com" |
| e03 | TaskCreated       | s1 / t1 / –  | goal="查天气 + 发邮件", budget={maxTokens:8000} |
| e04 | TurnStarted       | s1 / t1 / r1 | index=0 |
| e05 | LLMRequested      | s1 / t1 / r1 | model=claude-opus-4-7 |
| e06 | LLMReplied        | s1 / t1 / r1 | tool_calls=[weather] |
| e07 | ToolCalled        | s1 / t1 / r1 | name=weather, args={city:北京,date:2026-07-10} |
| e08 | ToolReturned      | s1 / t1 / r1 | {temp:26,sky:多云} |
| e09 | TurnEnded         | s1 / t1 / r1 | tokens_in=520, tokens_out=48 |
| e10 | TurnStarted       | s1 / t1 / r2 | index=1 |
| e11 | LLMRequested      | s1 / t1 / r2 | – |
| e12 | LLMReplied        | s1 / t1 / r2 | tool_calls=[send_email] |
| e13 | ToolCalled        | s1 / t1 / r2 | name=send_email, args={to:alice,body:...} |
| e14 | ToolReturned      | s1 / t1 / r2 | ok:true |
| e15 | TurnEnded         | s1 / t1 / r2 | – |
| e16 | TurnStarted       | s1 / t1 / r3 | index=2 |
| e17 | LLMReplied        | s1 / t1 / r3 | text="已经发送提醒邮件给 Alice。" |
| e18 | TurnEnded         | s1 / t1 / r3 | – |
| e19 | TaskEnded         | s1 / t1 / –  | status=succeeded |

因果链（`CausedBy`）：
- e02 由 e01 触发（会话开着才能说话）
- e03 由 e02 触发（用户说话催生任务）
- e07 由 e06 触发（LLM 决定调工具）
- e13 由 e12 触发；e12 又能沿 e11→…→e02 追到源头

对应实现见 `runtime/domain/ch01_sample.go`，回放测试见 `runtime/domain/ch01_sample_test.go`。
