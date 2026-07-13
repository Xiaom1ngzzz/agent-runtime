# 第 8 章 · 执行器

> ch02 的 `Executor.Run(turn)` 只是接口;真逻辑一直住在 `memfakes`。这一章把它扶正:**工具注册表、绑定失败、超时、可选并行**,并保持"从事件流读 LLMReplied"的续跑语义。

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

**刻意不把 `LLMResponse` 当参数**——唯一真相在事件流。进程崩溃后,新 Executor 实例仍可从 Store 续跑。

---

## 8.3 设计

### 8.3.1 Registry

```go
type Registry struct { /* name → ToolFunc + domain.Tool */ }
func (r *Registry) Register(desc domain.Tool, fn ToolFunc)
func (r *Registry) Descriptions() []domain.Tool  // 喂给 ContextEngine
```

一份注册表同时服务 Project(工具 schema)与 Emit(实现)。

### 8.3.2 ToolExecutor

```go
type ToolExecutor struct {
    Store     state.EventStore
    Registry  *Registry
    Timeout   time.Duration  // 单工具;0 = 只尊重 ctx
    Parallel  bool
    Snapshots snapshotSource // 可选;Store 不暴露 Snapshot 时显式注入(见 §8.6 取舍)
}
```

### 8.3.3 `ToolBindFailed`

```go
EvtToolBindFailed / PayloadToolBindFailed{CallID, Name, Reason}
```

仍追加 `ToolReturned{IsError}`,保证消息序列对 LLM 合法(role=tool 有对应 call)。

### 8.3.4 submit / resume(设计预留)

长工具 / HITL 需要把 Turn 拆成 submit+resume(ch01/ch02 前瞻)。Round 2 **不实现**拆分;接口仍是一次 `Run` 返回完整 Event 列表。超时视为该 call 失败,不挂起 Turn。

> **实现状态**:并行开关已落地;tokenizer、动态按 Event 注册工具留扩展。

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

断言:已知工具成功、未知工具出 `ToolBindFailed`、超时工具 `IsError`。

---

## 8.5 与 Runtime.Step 的关系

`Runtime.Step` 在 Chat 之后若 `len(ToolCalls)>0` 调 `Executor.Run`,再逐条 Append。换用 `ToolExecutor` 只需换依赖注入;memfakes.Executor 仍可用于旧测试。

---

## 8.6 取舍记录

| 决策 | 选择 | 代价 | 推翻条件 |
|------|------|------|----------|
| 失败可见性 | BindFailed + ToolReturned | 多一条 Event | 若上游只关心 ToolReturned,可折叠 |
| 超时 | per-call context | 不表达"部分完成挂起" | 长工具引入 submit/resume |
| 并行 | 可选 Parallel | 顺序与 LLM 列出顺序可能不同 | 要求保序时关 Parallel |
| 读 LLMReplied | 从 Store Snapshot | 需 Snapshot 能力 | 生产 Store 提供按 Turn 索引 |

---

## 8.7 小结

- Executor 是 Emit 段的生产实现:注册、超时、绑定失败、可选并行。
- 事件流仍是 tool call 的唯一来源。
- 下一章 **第 9 章 · 检查点与恢复** 把 Snapshot 升级为可跨进程的 Checkpoint。

---

## 参考

- Go: [`runtime-go/executor/`](../runtime-go/executor/)
- Rust: [`runtime-rs/src/executor/`](../runtime-rs/src/executor/)
- 相关:`ch02` §2.4、`ch07`、`ch09`
