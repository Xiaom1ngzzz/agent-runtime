# 第 8 章 · 执行器

> ch02 的 `Executor.Run(turn)` 只是接口;真逻辑一直住在 `memfakes`。这一章把它扶正为可测试的工具执行器。Go 参考实现包含注册表、绑定失败、合作式超时与可选并行;Rust 参考实现当前是同步、顺序基线。

---

## 8.1 问题:memfake 不够用的四个理由

1. **未知工具静默变错误字符串**。生产上要区分"工具没注册"与"工具执行失败"——前者是配置事故(`ToolBindFailed`),后者是业务错误。
2. **没有超时**。慢工具拖死整个 Turn,观测上只看到 Turn 很长。
3. **无法并行**。同一次 `LLMReplied` 里多个互不依赖的 tool call 被串行化。
4. **工具描述与实现脱节**。Context 里的 `Tools[]` 与 Executor 查表各维护一份,容易漂移。

---

## 8.2 概念:Emit 段的边界

回顾 ADR-002 五段:`Fold → Project → Compile → Chat → Emit`。

Executor 只负责 **Emit**:

```
读 Turn 内最新 LLMReplied.ToolCalls
  → 对每个 call: ToolCalled → (执行) → ToolReturned
  → 未知:额外 ToolBindFailed
```

**刻意不把 `LLMResponse` 当参数**——tool call 的来源是事件流。但当前 `Run` 会先执行工具、再把整组 Event 交给 Runtime 追加;若外部副作用成功后、`ToolReturned` 落库前崩溃,恢复时可能重复调用。因此本轮只能安全续跑幂等工具;生产实现还需要 submit/resume、CallID 去重与 outbox。

---

## 8.3 设计

### 8.3.1 Registry

**Go**

```go
type Registry struct { /* name → ToolFunc + domain.Tool */ }
func (r *Registry) Register(desc domain.Tool, fn ToolFunc)
func (r *Registry) Descriptions() []domain.Tool  // 喂给 ContextEngine
```

**Rust**

```rust
pub struct Registry { /* name → ToolFn + Tool */ }
impl Registry {
    pub fn register(&self, desc: Tool, fn_: ToolFn);
    pub fn descriptions(&self) -> Vec<Tool>; // 喂给 ContextEngine
}
```

一份注册表同时服务 Project(工具 schema)与 Emit(实现)。

**工具校验与输出限制(生产)**:

- **入参**:Executor 在 dispatch 前按 `domain.Tool` 的 JSON Schema 校验 `arguments`;校验失败 → `ToolReturned{IsError}` + 可选 `ToolValidationFailed`(扩展 EventType),不调用实现函数。
- **出参**:对返回内容设上限(如 64KB 文本 / 深度 ≤ 3 的 JSON);超限截断并标记 `truncated=true`,防止单轮 tool result 撑爆 Context 窗口。
- **副作用类工具**:须在注册时声明 `side_effect` 级别,供 Planner/审计层区分 read-only 与 mutating 调用。

Round 2 参考实现仅覆盖"未知工具 → BindFailed";schema 校验与输出截断留 ch11 / ADR-008 扩展。

### 8.3.2 ToolExecutor

**Go**

```go
type ToolExecutor struct {
    Store     state.EventStore
    Registry  *Registry
    Timeout   time.Duration  // 单工具;0 = 只尊重 ctx
    Parallel  bool
    Snapshots snapshotSource // 可选;Store 不暴露 Snapshot 时显式注入(见 §8.6 取舍)
}
```

**Rust**

```rust
pub struct ToolExecutor<S: SnapshotStore> {
    pub store: Arc<Mutex<S>>,
    pub registry: Arc<Registry>,
    pub timeout: Option<Duration>, // 单工具;None = 不额外超时
}
// Rust 参考实现当前为同步顺序调用;Parallel 留扩展
```

### 8.3.3 `ToolBindFailed`

**Go**

```go
EvtToolBindFailed / PayloadToolBindFailed{CallID, Name, Reason}
```

**Rust**

```rust
// EVT_TOOL_BIND_FAILED / PayloadToolBindFailed { call_id, name, reason }
```

仍追加 `ToolReturned{IsError}`,保证消息序列对 LLM 合法(role=tool 有对应 call)。

### 8.3.4 submit / resume(设计预留)

长工具 / HITL 需要把 Turn 拆成 submit+resume(ch01/ch02 前瞻)。Round 2 **不实现**拆分;接口仍是一次 `Run` 返回完整 Event 列表。超时视为该 call 失败,不挂起 Turn。

> **实现状态**:Go 的并行开关与 context 超时已落地;Go 并行结果会按模型给出的 call 顺序重组。Rust 当前只实现同步顺序调用,`timeout` 字段尚未执行超时。submit/resume、持久化调度和动态按 Event 注册工具留作扩展。

---

## 8.4 参考实现

**Go**

```
runtime-go/executor/executor.go      # Registry + ToolExecutor
runtime-go/executor/ch08_executor_test.go
```

**Rust**

```
runtime-rs/src/executor/mod.rs
runtime-rs/tests/ch08_executor.rs
```

```bash
cd runtime-go && go test ./executor -run TestCh08 -v
cd runtime-rs && cargo test ch08_tool_executor
```

Go 测试断言:已知工具成功、未知工具出 `ToolBindFailed`、合作式超时工具 `IsError`。Rust 测试当前断言前两项,不把未实现的超时/并行当作已验证能力。

---

## 8.5 与 Runtime.Step 的关系

`Runtime.Step` 在 Chat 之后若 `len(ToolCalls)>0` 调 `Executor.Run`,再逐条 Append。换用 `ToolExecutor` 只需换依赖注入;memfakes.Executor 仍可用于旧测试。

这个调用顺序也标出了当前参考实现的崩溃窗口:工具执行期间 `ToolCalled` 尚未持久化。它适合教学和幂等工具,不应被解释为 exactly-once 副作用协议。可恢复的生产协议应先追加调用意图,再以 `CallID` 作为幂等键执行,最后追加结果。

---

## 8.6 取舍记录

| 决策 | 选择 | 代价 | 什么情况下会被推翻 |
|------|------|------|----------|
| 失败可见性 | BindFailed + ToolReturned | 多一条 Event | 若上游只关心 ToolReturned,可折叠 |
| 超时 | per-call context | 不表达"部分完成挂起" | 长工具引入 submit/resume |
| 并行 | Go 可选 Parallel,Rust 顺序 | Go 返回 Event 仍按 call 顺序重组;完成时序不可见 | 需要流式完成事件或 Rust 并行时升级接口 |
| 读 LLMReplied | 从 Store Snapshot | 需 Snapshot 能力 | 生产 Store 提供按 Turn 索引 |

---

## 8.7 小结

- Executor 是 Emit 段的参考实现:两端都有注册与绑定失败;超时和可选并行目前仅 Go 落地。
- 事件流仍是 tool call 的唯一来源。
- 当前一次性 `Run` 不消除"副作用成功、结果未落库"窗口;生产环境需幂等工具或 submit/resume + outbox。
- 下一章 **第 9 章 · 检查点与恢复** 把 Snapshot 升级为可跨进程的 Checkpoint。

---

## 参考

- Go: [`runtime-go/executor/executor.go`](../runtime-go/executor/executor.go)
- Rust: [`runtime-rs/src/executor/mod.rs`](../runtime-rs/src/executor/mod.rs)
- 时序图:[`diagrams/ch08-executor-sequence.mmd`](../diagrams/ch08-executor-sequence.mmd)
- 相关:`ch02` §2.4、`ch07`、`ch09`
