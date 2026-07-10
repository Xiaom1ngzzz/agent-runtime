# ADR-003: Runtime 与 DDD 概念的对应关系

- **状态**: Accepted
- **日期**: 2026-07-10
- **决策者**: —
- **上游**: [ADR-001 · Runtime 边界与职责](ADR-001-runtime-domain.md), [ADR-002 · Runtime 数据流协议](ADR-002-dataflow-protocol.md)
- **落地章节**: ch01, ch02, ch03, ch04(以及后续所有章节)

## Context

从 ch01 开始一路推导过来,读者和贡献者会自然生出一个问题:

> 这套设计是不是某种成熟的模式?能不能给它一个名字?

答案是**是**——Runtime 大量融入了 **Domain-Driven Design(DDD)** 思想,并且是 DDD 的一个特定子集:**Event Sourcing + CQRS + Bounded Context**。但**我们不是"套 DDD 模式"倒推设计**,而是从第一原理推导出来(ch01 §1.1 的 7 个痛点 → ch01 §1.2 事件优先决策 → ADR-001 边界)之后,发现结果与 DDD 高度收敛。

**"顺路符合"比"刻意套用"更有说服力**。这条 ADR 把对应关系沉淀成一张表,并说明:

1. 我们采用了 DDD 的**哪些**部分,分别体现在哪里
2. 我们**没有**采用 DDD 的哪些部分,以及为什么
3. 未来演进时,这张表会怎么变

## Decision

### 对应关系(全书总表)

| DDD 概念 | 在本 Runtime 中的体现 | 参考位置 |
|---|---|---|
| **Ubiquitous Language** | 严格建立 `Session / Task / Turn / Event` 四词术语;`ch01 §1.7` 专门对比 OpenAI 的 `Thread/Run/Step`、LangGraph 的 `State/Checkpoint`、AutoGen 的 `GroupChat` 等命名并逐条说明为什么不采用 | ch01 §1.3, §1.7 |
| **Bounded Context** | Runtime 边界 —— 明确哪些**在** Runtime 内(Session/Task/Turn/Event 生命周期、Context 组装、State/事件、工具调度、Trace);哪些**不在**(LLM Provider、Tool 实现、Storage 后端、UI) | ch01 §1.5, ADR-001 |
| **Entity** | `Session`、`Task`、`Turn` —— 有全局唯一 ID,有生命周期(pending → running → succeeded/failed/canceled/timeout) | ch01 §1.3 |
| **Value Object** | `Event`、`Message`、`ToolCall`、`Summary`、`Decision`、`Progress`、`Step`、`TurnDigest`、`MemoryRef`、所有 `Payload*` —— 全部不可变、按值比较、无独立 ID(只是可能内嵌 Entity ID 作为字段) | ch01 §1.3, ch04 §4.6 |
| **Aggregate Root** | `Session` —— 所有 `Task / Turn / Event` 通过 `SessionID` 归属其下;EventStore 的 append/apply 按 `session_id` 分锁串行,跨 Session 并行(ch03 §3.4.2) | ch01 §1.4, ch03 §3.4 |
| **Aggregate 读模型 (Read Model)** | `SessionView` —— Fold 出的只读投影。是 CQRS 的读侧;它不代表"当前状态的真理",Event 流才是 | ch03 §3.5 |
| **Domain Event** | `Event` 本身。ch01 §1.2 的"事件优先"决策 = 教科书级 Event Sourcing;Event 是 Runtime 的唯一真理来源 | ch01 §1.2 |
| **Event Sourcing** | 显式采纳。约束 C1(单向)与 C2(幂等)= Event Sourcing 的两条铁律。ch03 §3.2 明确用这两条约束推出后面所有设计 | ch03 §3.2 |
| **CQRS(Command Query Responsibility Segregation)** | `State.Apply`(command 侧,写)与 `State.View`(query 侧,读)在接口层分离;ADR-002 明确 Fold/Project/Compile 是纯函数,Chat/Emit 有副作用——分开推理各自的可变性 | ch03 §3.5, ADR-002 |
| **Repository** | `EventStore` / `SnapshotStore` / `SummaryStore` / `MemoryStore`(ch05)—— 都是"接口 + 可换实现",持有聚合的持久化职责,业务层不直接接触存储细节 | ch03 §3.4, §3.6, ch04 §4.10, ch05 |
| **Domain Service** | `Compressor`(ch04 §4.5)—— 跨聚合协调 `State + Store + Summarizer`,行为不属于任一 Entity 或 Value Object;`PromptCompiler`(ch06)同理 | ch04 §4.5, ch06 |
| **Anti-Corruption Layer(ACL)** | `Summarizer` trait 隔离 LLM 调用(把不确定性挡在 Compressor 边界);`PromptCompiler` 的 Provider Adapter 隔离 LLM 厂商差异(OpenAI/Anthropic/Bedrock 用同一个内部表达) | ch04 §4.5, ch06 §6.6 |
| **Snapshot Pattern** | `Snapshot` + `SnapshotStore`,Turn 边界拍照;ES 的经典优化手法,加速崩溃恢复 | ch03 §3.6 |

### 我们**没有**采用的 DDD 部分

以下概念在本 Runtime **不引入**,理由要么是"当前规模不需要",要么是"引入反而妨碍推理"。这不是遗漏,是**明确的取舍**:

| DDD 概念 | 未采用理由 | 什么情况下会引入 |
|---|---|---|
| **Command 显式建模** | 目前 Command 是接口方法调用(`Runtime.Step`、`Compressor.Tick`),还不是独立的 Value Object。**单进程内没必要**——把方法参数打包成 struct 只会增加样板。 | 若引入远程调用(gRPC/REST)、命令排队、命令审计,值得把 Command 独立建模 |
| **Factory 模式** | Constructor 直接够用。Go 用 `NewX()` 函数,Rust 用 `impl X { pub fn new() }`,已经是事实上的 Factory。 | 若构造涉及复杂依赖注入或多态选择,再引入 Factory |
| **Specification 模式** | 我们的过滤逻辑都是简单的 filter function(如 `activeTurnSet`、`selectRelevantSummaries`)。抽象成 Specification 反而绕。 | 若过滤规则需要组合(and/or/not)、需要持久化、需要跨 Bounded Context 共享,再引入 |
| **Domain Event vs Integration Event 区分** | 目前只有一层 Event —— Domain Event 本身。没有跨 Bounded Context 发布订阅。 | 若引入 Runtime 之外的下游订阅方(Analytics、Billing 独立服务),需要区分"内部 Domain Event"与"对外 Integration Event" |
| **Saga / Process Manager** | 目前 Task 是扁平的(ch01 §1.9 明确"本章按扁平处理")。跨 Task 协调没有需求。 | ch07 Planner & Task Graph 引入 sub-Task 时,协调子任务成败的组件将扮演 Saga/Process Manager 角色。届时新增 ADR |
| **Context Mapping** | 目前只有一个 Bounded Context(Runtime),不需要考虑 BC 间的 relationship(Shared Kernel / Customer-Supplier / ACL 等) | 若拆分 Runtime 为多个 BC(如 Session Management BC 与 Execution BC 分家),需要显式做 Context Mapping |

### 反例:我们特别拒绝的两种"看起来像 DDD"的做法

**反例 1: 把 SessionView 当成 Entity 直接修改**

看起来是"面向对象",但违反 ES 的 C1(单向):

```go
// ❌ 反例
view := state.View(sid)
view.Tasks["t1"].Status = domain.TaskSucceeded  // 就地修改
state.Save(view)                                  // "保存"聚合
```

问题:
- 状态变化没落到 Event 流里,回放失败
- 并发修改需要显式加锁,复杂度爆炸
- 与 ch01 §1.2 的"事件优先"决策直接冲突

**正确做法**:追加 `TaskEnded` Event,让 Fold 自然把状态推到 `Succeeded`。见 ch03 §3.5。

**反例 2: 把 Event Payload 用 `map[string]any` / `serde_json::Value`**

看起来是"Value Object 的松散建模",实际把类型检查推到运行时:

```go
// ❌ 反例
type Event struct {
    Type    string
    Payload map[string]any  // 任何东西都能塞
}
```

问题:
- 消费方拼错字段名要到线上才发现
- schema 演进无据可查
- 序列化时不知道 payload 结构

**正确做法**:Go 用 marker interface + `Payload*` struct,Rust 用封闭 enum;编译器强制类型化。见 ch01 §1.3, ch03 §3.3.2。

## Consequences

### 正向

- **术语稳定**:Session/Task/Turn/Event/Aggregate/View/Command/Repository 这套词汇让新读者能快速定位每一段代码的语义角色
- **易于向 DDD 熟悉的团队解释**:说"我们用 Event Sourcing + CQRS,Session 是 Aggregate Root"比说"我们把状态变化都记成事件、只读投影用 Fold 生成"更容易在架构评审里通过
- **未来重构方向清晰**:何时引入 Saga / Command / Factory 都有明确触发条件(见表格右列),不需要"感觉需要就加"
- **兼容 DDD 生态工具**:Event Sourcing 的成熟工具(EventStoreDB、Axon Server、AWS EventBridge 等)可以作为 EventStore 的 L4 档次实现,不需要重新发明轮子
- **测试友好**:纯函数(Fold/Project/Compile)= 单元测试极简;有副作用的段(Chat/Emit)有 ACL 隔离,可换 fake

### 负向 / 需要接受的取舍

- **对未接触过 DDD 的读者有认知门槛**:书里不能假设读者懂 DDD,所有 DDD 术语都要在第一次出现时用第一原理解释一遍
- **过度设计的风险**:小规模场景下,Aggregate / Repository / Domain Service 分层可能显得繁琐。缓解方式:参考实现分档(memfakes / 生产实现)、明确"什么规模适用什么档次"
- **Command 未显式建模的债务**:未来若接入 RPC/CLI,需要一次性把方法调用打包成 Command Value Object,会有一次较大重构
- **多 Bounded Context 未来的复杂度**:书里目前只讲一个 BC,读者若把 Runtime 拆解成多 BC 部署,需要额外的 Context Mapping 章节(暂未规划)

## Alternatives

**A. 完全不提 DDD,靠术语自解释**

- 优点:第一原理推导本身足够清晰,读者不需要额外学习 DDD
- 缺点:与 DDD 熟悉的架构师沟通成本高;失去"这不是自造术语,是成熟模式"的说服力
- **未选择理由**:读者群体中不少人接触过 DDD 或 Event Sourcing;明确对应关系能让他们**跳过重复学习**,直接聚焦本书的独特贡献(Runtime 特有的问题与决策)

**B. 从一开始就用 DDD 术语讲**

- 优点:更简洁,不需要"从痛点推出 Event Sourcing"这样绕
- 缺点:读者被迫先学 DDD 才能读本书;违反本书"面向工程、从第一原理讲清楚"的写作原则(见 BOOK.md)
- **未选择理由**:本书目标读者是"正在从零构建 Agent 系统的工程师",不是"DDD 老手"。先讲痛点、再给对应关系,认知负担最低

**C. 采用 DDD 的完整体(含 Saga / Context Mapping / Domain Service 全套)**

- 优点:未来演进时不需要"补交作业"
- 缺点:当前规模用不到,读者会被大量"暂时无用"的抽象干扰
- **未选择理由**:与 ch01 §1.9 的取舍哲学一致:**不做未来才需要的设计**。触发条件(见"未采用"表)清晰,到时再引入不迟

**D. 换一套自造的模式命名(不引用 DDD)**

- 优点:表达完全贴合本书语境
- 缺点:与业界脱节;别人读到 EventStore 会想 EventStore,读到本书自造名字要重新学
- **未选择理由**:DDD 词汇已经是行业公共语言,借用而不发明,降低沟通成本

## References

- Eric Evans, *Domain-Driven Design: Tackling Complexity in the Heart of Software* (2003)
- Vaughn Vernon, *Implementing Domain-Driven Design* (2013)
- Greg Young, *CQRS Documents* (2010) —— CQRS 与 Event Sourcing 的经典阐述
- Martin Fowler, *Event Sourcing* (2005)、*CQRS* (2011)
- 本书:
  - [ADR-001 · Runtime 边界与职责](ADR-001-runtime-domain.md)
  - [ADR-002 · Runtime 数据流协议](ADR-002-dataflow-protocol.md)
  - `ch01 §1.2` 事件优先决策(≈ Event Sourcing 的动机)
  - `ch01 §1.3` 四层对象(≈ Entity / Value Object 的落地)
  - `ch01 §1.7` 与其他框架命名的对比(≈ Ubiquitous Language 的实践)
  - `ch03 §3.2` C1(单向) + C2(幂等) —— ES 两条铁律的形式化
  - `ch03 §3.5` SessionView 作为读模型
  - `ch04 §4.5` Compressor 作为 Domain Service
