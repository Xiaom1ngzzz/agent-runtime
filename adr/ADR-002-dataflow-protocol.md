# ADR-002: Runtime 数据流协议

- **状态**: Accepted
- **日期**: 2026-07-09
- **决策者**: —
- **上游**: [ADR-001 Runtime 边界与职责](ADR-001-runtime-domain.md)
- **落地章节**: [ch02 Runtime Data Flow](../chapters/ch02-runtime-dataflow.md)

## Context

ADR-001 划定了 Runtime 的边界(接收任务、驱动 Agent、返回结果),也划出了 6 个接口:`ContextEngine / PromptCompiler / LLMProvider / Executor / State / EventStore`。

但**接口签名对 ≠ 协作方式对**。同样这 6 个接口,用不同方式串起来会得到不同语义的 Runtime:

- 有的实现会让 ContextEngine 主动做外部调用("顺便查一下最新天气"),把副作用埋进 Project。
- 有的实现会让 PromptCompiler 读 State,让 Prompt 不再是 Context 的纯函数。
- 有的实现会让 EventStore.Append 与 State.Apply 之间存在时间差,导致 Fold 读到滞后的视图。

ch02 把这些反例列了出来,并说明为什么每一种都会踩到 ch01 §1.1 的痛点(尤其是"回放"和"审计")。**需要一份正式的、可被其它章节和 ADR 引用的协议,把这些约束固化下来。**

## Decision

Runtime 内部的一次 Turn 由以下**5 段单向数据流**构成,每段的输入/输出类型与副作用规则如下:

### 转换定义

| 段 | 输入 → 输出 | 承担者 | 纯度 |
|---|---|---|---|
| **Fold**    | `[]Event → SessionView`               | `State.Apply` + `State.View` | 纯 |
| **Project** | `SessionView → Context`（可只读展开 EventStore 中的消息原文,见下） | `ContextEngine.Assemble`     | 纯 |
| **Compile** | `Context → Messages`                  | `PromptCompiler.Compile`     | 纯 |
| **Chat**    | `Messages → LLMResponse`              | `LLMProvider.Chat`           | 有副作用(网络) |
| **Emit**    | `LLMResponse + ToolResult → []Event`  | `Executor.Run`               | 有副作用(工具) |

**"纯"** 的定义:同样输入必然同样输出,不发起外部请求,不修改 Runtime 之外的任何状态,不依赖 Runtime 之外的可变状态(时钟、随机数需通过参数传入,不允许模块内部读取)。

### 数据流方向

```
[]Event  →  SessionView  →  Context  →  Messages  →  LLMResponse  →  []Event
   ▲                                                                     │
   └─────────────────────────────────────────────────────────────────────┘
```

**箭头是单向的**。这条规则展开为下面 5 条约束:

1. **Fold 只写 State,不读外部**。`State.Apply` 的输入必须是从会话起点(或某个 Checkpoint)开始的完整 Event 流;截断折叠是非法的。
2. **Project 以 SessionView 为主输入,不写 Event、不发外部请求**。生命周期判断(哪个 Task/Turn 在跑)只能来自 Fold 后的 SessionView,不能绕过 State 直读 EventStore。若需要 LLM 消息原文,可对 EventStore 做**只读**加载,按 SessionView.WorkingSet 等字段展开已提交的事实——不是"第二套状态源"。ContextEngine 若需要"新鲜数据",必须让 LLM 通过 ToolCall 触发,不能自作主张。
3. **Compile 只读 Context,不读 State、不发外部请求**。同样的 Context 必须产出同样的 Messages;LLM 不同 Provider 的差异化编排(如 system 位置、tool schema 格式)也发生在 Compile 内部,不外泄。
4. **Chat 只知道 Messages 与 Tools**。LLM Provider 不感知 State,不感知 Session/Task/Turn 的 ID;这让 Provider 可插拔。
5. **Emit 只追加 Event,不修改 Context / State 中已有对象**。Event 一旦追加就不可变(§ADR-001);要修正过去,只能追加新的 Event(例如 `ToolResultOverridden`)。

### Runtime 协调器义务

任何声称实现本 Runtime 协议的协调器必须:

- **按顺序**执行 Fold+Project → Compile → Chat → Emit;不允许并行,不允许调换。
- **每追加一条 Event 都紧接着 Apply 到 State**——`EventStore.Append` 与 `State.Apply` 在协调器内成对、顺序执行;生产实现应在同一 session 锁内完成(见 ch03 §3.4.4 方案 A)。参考实现为教学清晰使用两把锁,逻辑上仍保证 Step 内无中间态。
- **不承担生命周期事件**:Session/Task/Turn 的 `Open/Create/Start/End` 由调用方(上层 Loop)追加。协调器只负责"给定一个已就绪的 Turn,把它跑完"。
- **不承担重试**:Chat 失败直接返回错误;重试策略在上层 Loop。
- **每段转换必须是可观测的一个 Span**(见 [ch02 §2.6](../chapters/ch02-runtime-dataflow.md#26-span))。

### 失败模型

| 段 | 失败策略 | 是否终止 Turn |
|---|---|---|
| Fold    | 拒绝服务;不猜、不跳过。Event 流损坏 → `TaskEnded{failed}`。 | 是 |
| Project | 记录 Event,降级到"最小 Context"。 | 否 |
| Compile | 记录 Event,拒绝本 Turn 的 LLM 调用。 | 是 |
| Chat    | 返回错误;上层 Loop 决定是否重试。 | 是 |
| Emit    | 单个 ToolCall 失败 = `ToolReturned{is_error=true}`;不回滚已发生的副作用。 | 否 |

**核心哲学**:**不承诺原子性,承诺可审计**。Runtime 不保证工具副作用可以回滚;它保证每一次副作用都在 Event 流里如实记录,下一个 Turn 或 Human-in-the-loop 可以基于事实决定补救。

## Consequences

### 正向

- **回放可复现**:因为 Fold/Project/Compile 是纯函数,给同一份 Event 流必然折叠出同样的 SessionView 与 Context。ch09 Checkpoint 的可行性由此获得。
- **Provider 可替换**:因为 Chat 只依赖 Messages+Tools,换 LLM 换 Provider 不改上下游。
- **观测点自然对齐**:5 段转换 = 5 个 Span 边界,与 ch10 的观测 Attribute 规范一一对应,不用事后打补丁。
- **审计有据**:副作用只能通过 Emit 落到 Event 流,任何"这个工具是谁触发的"都能沿 `CausedBy` 链回溯。
- **契约与代码合一**:6 个接口的类型签名同时是本协议的形式化表达,编译器帮你检查一半。

### 负向 / 需要接受的取舍

- **写协调器代码变啰嗦**:显式的 `Append + Apply` 每次两步、每段转换要单独打 Span、生命周期事件要调用方追加。相比"业务代码里随手调 LLM",工程量大。
- **纯度约束限制了实现自由度**:ContextEngine 不能"顺便"发外部请求,所有 IO 必须走 Tool。灵活性有代价。
- **失败模型不保证原子性**:副作用一半成功时,只有事实记录、没有自动回滚。上层业务要基于 Event 流做补救逻辑。
- **不支持流式 / 长时任务**:本版本 `Step` 一次跑到底。Streaming、Human-in-the-loop 需要 ch08 把 `Step` 拆成 `submit + resume`。

## Alternatives

**A. 不定义协议,让每个团队按 6 个接口自己拼**
- 优点:上手快,不用读 ADR。
- 缺点:回到 ch02 §2.1 的四类症状,每个团队踩一遍相同的坑。每次协作时不知道对方的 Runtime 语义,集成成本高。
- **未选择理由**:违背本书"面向演进"的原则——没有协议就没有可讨论的演进对象。

**B. 让 Runtime 内建重试、Budget、生命周期管理(更"厚"的协调器)**
- 优点:demo 用起来爽,一行 `Runtime.Run(userInput)` 搞定。
- 缺点:重试语义与 Task 语义耦合;Budget 检查逻辑塞进 Step 后,`Step` 变成"什么都做"的黑盒,失败模型更难说清。
- **未选择理由**:违背 ADR-001 的"边界最小"原则。这些能力放上层 Loop 是可组合的,内建则是绑定的。

**C. 允许 Project 有副作用(比如"懒加载向量库")**
- 优点:某些实现能省一次 ToolCall。
- 缺点:回放变得不确定;并发压测时副作用可能重复触发。
- **未选择理由**:代价压过收益。如果确有必要,应作为**单独的 ADR 破例**(见 ch01 §1.9),只对该子集破例,不动整体规则。

**D. 每段转换用消息队列解耦(actor 模型)**
- 优点:并行度理论上更高。
- 缺点:Fold/Project/Compile 都很快(通常毫秒级),消息队列反而是性能瓶颈;因果链的追踪成本大幅上升。
- **未选择理由**:过度工程化。ch08 讨论多 Agent 时会重新评估,但**单 Agent 单 Turn 的场景下,同步管道够用且更好推理**。

## References

- ch01 §1.2 事件优先决策
- ch01 §1.6.1 样本 Event 流
- ch02 §2.3 四种转换的类型签名
- ch02 §2.5 单向数据流的反例
- ch02 §2.7 失败与退化表
- 参考实现:
  - Go: [`runtime-go/runtime/runtime.go`](../runtime-go/runtime/runtime.go)
  - Rust: [`runtime-rs/src/runtime.rs`](../runtime-rs/src/runtime.rs)
- 端到端验证:
  - `go test ./examples/ch02`
  - `cargo test ch02`
