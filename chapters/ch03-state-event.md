# 第 3 章 · 状态与事件模型

> 第 1 章讲了世界里有哪些名词,第 2 章讲了数据怎么在里面流动。这一章往里钻一层:**Event 到底怎么存、State 到底怎么算、二者又如何一起活下来**——落到 schema、序列化、并发、快照、恢复这些具体问题上。

---

## 3.1 问题:能跑的 fake,扛不住的现实

ch02 §2.8 的 `memfakes` 已经能跑一次完整的 Turn,20 条 Event 端到端产出。但那是**内存 + 单进程 + 剧本化 LLM** 三重理想条件叠加的结果。任何一条挪走,下面这些事就会一次性砸下来:

1. **进程重启 = 全部消失**。`memfakes.EventStore.events` 是一个 `[]Event` 切片,进程一挂,20 条 Event 一起蒸发。
2. **Payload 落盘怎么写**。Go 的 marker interface / Rust 的封闭 enum 编译期很干净,但 `json.Marshal(e.Payload)` 出来只是 payload 内部字段——**再反序列化时,`EventPayload` 这个接口/enum 怎么知道要构造哪个具体类型**?
3. **并发追加序号错乱**。两个协程各自持有一个 `Event`,同时 `Append`——谁在前?ID 谁分配?如果 ID 是 `"e" + counter`,两次同号就是灾难。
4. **Apply 慢了半拍**。ch02 要求协调器内 `Append` 后立刻 `Apply`。真实实现里,如果两把锁之间插入并发读,`State.View` 拿到的视图可能比 `EventStore.Load` 少一条——Turn 状态卡在 `Running`,下一次 `Step` 前置检查失败,业务体感是"卡住"(见 §3.4.4)。
5. **Load 越滚越慢**。Session 跑了三天累积 3 万条 Event,每次 `Assemble` 都要 Load 全量、从头 Fold。热路径 200ms → 2s。
6. **Schema 演进 = 生产事故**。上周 `PayloadTurnEnded` 加了 `CostUS` 字段;这周想读上周的 Event,序列化字段名对不上,反序列化直接 panic。
7. **未知 EventType 怎么办**。回滚了服务版本,但事件流里已经有新版才有的 `PayloadSubTaskSpawned`——旧代码不认识这个 variant,是拒绝服务还是跳过?

这 7 件事,ch02 的数据流协议没直接回答。这一章把它们逐条落成 **schema、契约、实现**。

**这一章不是给你一份"标准实现"** ——每个团队用的存储(Postgres / SQLite / EventStoreDB / 自制 KV)都不同。目标是**把不变量固定下来**:哪些是随存储实现变化的,哪些是不管怎么换存储都不能破的。

---

## 3.2 概念:五个词,一张图

在 ch01 §1.2 事件优先决策之上,再定义几个具体概念。它们是本章接下来所有实现的词根。与 DDD / Event Sourcing / CQRS 的对应关系见 [ADR-003](../adr/ADR-003-ddd-mapping.md)。

```
                                Turn 边界
                                    ↓
┌───────────┐   append   ┌────────────────────┐   fold    ┌──────────────┐
│  Runtime  │──────────▶│    EventStore      │──────────▶│  SessionView │
│  (§2.4)   │            │  (append-only log) │           │  (只读视图)   │
└───────────┘            └────────────────────┘           └──────────────┘
                              │        ▲
                              │        │  save / load
                              ▼        │
                          ┌──────────────┐
                          │  Snapshot    │
                          │  (offset, V) │
                          └──────────────┘
```

- **Event** — 唯一的可变载体。所有状态变化 = 追加一条 Event。见 §3.3。
- **EventStore** — 只支持 `Append` 和 `Load(from_offset)` 的 append-only log。见 §3.4。
- **State** — Event 流的**折叠函数**加上被折出的**只读 View**。`State = fold(events)`。见 §3.5。
- **Fold** — 纯函数 `(SessionView, Event) → SessionView`。可从零折,也可从 Snapshot 继续折。
- **Snapshot** — 某个 offset 处 View 的镜像,是恢复加速器。**Turn 边界是 Snapshot 的天然对齐点**。见 §3.6。

**约束**——记住这两条,后面所有取舍都是它俩的推论:

> **C1 · 单向**:Event 只能追加,不能改;State 只能读,写必须走 `Apply`。
>
> **C2 · 幂等**:`fold(events)` 是纯函数——同一份事件流,任何时候折叠得到同样的 View。

---

## 3.3 Event Schema:字段与不变量

先把结构定死。字段和 ch01 §1.3 一致,这里补齐每个字段的**不变量**——违反了就该在 `Append` 边界拒绝。

| 字段         | 类型   | 不变量                                                                    |
| ------------ | ------ | ------------------------------------------------------------------------- |
| `id`         | 字符串 | 全局唯一;由 EventStore 在 `Append` 时分配;客户端不预填                    |
| `session_id` | 字符串 | 非空                                                                      |
| `task_id`    | 字符串 | 可空(如 `SessionOpened`);非空时必须存在于同 session 的 `TaskCreated` 之后 |
| `turn_id`    | 字符串 | 可空;非空时必须在 `TurnStarted` 之后、`TurnEnded` 之前(§3.5 的因果链约束) |
| `type`       | 枚举   | 与 `payload` 的具体类型一一对应                                           |
| `payload`    | 类型化 | 见 §3.3.2                                                                 |
| `ts`         | 时间戳 | 由 EventStore 在 `Append` 时打;单调不减                                   |
| `caused_by`  | 字符串 | 可空;非空时必须指向此前已在同 session 中出现过的 Event id                 |

**为什么 `id` 与 `ts` 由 EventStore 分配,而不是调用方**:

- 客户端时钟不可信(见 ADR-002 §Decision 的"纯度"定义——时钟/随机数只能通过参数传入,`Fold`/`Project`/`Compile` 内部不能读)。
- 让 Append 端集中分配,就把"单调、唯一"的责任压在**一个位置**,而不是散在每个业务模块里。
- 后果:调用方递给 `Append` 的 Event 里 `id` 与 `ts` **必须留空**;EventStore 有权拒绝已填的输入(或按策略覆盖,但要留告警)。

### 3.3.1 逻辑时钟:seq 而不仅仅是 wall clock

`ts` 用于人读的日志和跨系统跟踪,但**不能作为排序键**——wall clock 有回退、集群不同步。EventStore 内部还需要一个**每 session 单调递增的 `seq`**(64 位整数),作为真正的顺序主键。

`seq` 是 `Event` 的一个字段(`domain.Event.Seq`,0 表示尚未分配),但与 `id`/`ts` 同一条规则:**由 EventStore 在 `Append` 时分配,调用方不预填**。`Load` 返回的顺序 = seq 升序;`Snapshot` 存的是 `(seq, view)`,恢复时"从 `seq+1` 开始 replay";§3.5.4 的 Apply 单调性校验也依赖它。

**为什么单独一个 `seq`,不复用 `id`**:

- `id` 是全局唯一的字符串(可能是 UUID / ULID / snowflake),排序需要额外解析;`seq` 是同 session 内的单调 int64,`ORDER BY seq` 一步到位。
- 跨 session 时无所谓相互序;同 session 内必须严格全序——`seq` 精准地表达"同 session 严格全序"这个约束。

### 3.3.2 Payload 的类型化与序列化

ch01 §1.3 已经论证过:Payload 必须是类型化的(Go marker interface / Rust 封闭 enum),而不是 `map[string]any` / `serde_json::Value`。这里补齐**落盘时怎么办**——因为编译期约束在 JSON/Protobuf 那头是不存在的。

**核心问题**:反序列化时,拿到一段 JSON `{...}`,`EventPayload` 要构造出哪个具体 struct?

**做法(两语言选一,本书两种都给)**:

**Go 端**——`type` 字段 + 分派表:

```go
// runtime-go/state/wire.go(实现见本章 §3.7)
type EventDTO struct {
    ID        string          `json:"id"`
    SessionID string          `json:"session_id"`
    TaskID    string          `json:"task_id,omitempty"`
    TurnID    string          `json:"turn_id,omitempty"`
    Type      domain.EventType `json:"type"`
    Payload   json.RawMessage `json:"payload"`
    TS        time.Time       `json:"ts"`
    CausedBy  string          `json:"caused_by,omitempty"`
    Seq       int64           `json:"seq"`
}

// payloadFactory 决定"type 字符串 → 空 payload 实例"
var payloadFactory = map[domain.EventType]func() domain.EventPayload{
    domain.EvtSessionOpened: func() domain.EventPayload { return &domain.PayloadSessionOpened{} },
    domain.EvtTaskCreated:   func() domain.EventPayload { return &domain.PayloadTaskCreated{} },
    // ...每种 EventType 一项;新增 EventType 必须加一行,否则反序列化报 unknown type 错误
}
```

**Rust 端**——`serde` adjacently tagged enum:

```rust
// runtime-rs/src/state/wire.rs
#[derive(Serialize, Deserialize)]
#[serde(tag = "type", content = "payload")]
pub enum EventPayloadWire {
    SessionOpened(PayloadSessionOpened),
    TaskCreated(PayloadTaskCreated),
    // ...每 variant 与 EventPayload 一一对应
}
```

Rust 版靠 `serde` 生成分派代码,编译器强制穷举——新增一种 `EventPayload` variant 忘了在 `EventPayloadWire` 里加,`impl From<EventPayload> for EventPayloadWire` 直接不编译。这比 Go 端**手工维护 factory 表**更安全。

**两个语言共同踩的坑**:

1. **`type` 字段的字符串是 wire format 的一部分**。改字段名 = schema break。改就要出 ADR + 迁移。
2. **未知 `type` 的处理策略**要在 wire 层显式定义(见 §3.8)。本书基线选择"如实报错":wire 层拒绝反序列化未知类型(Go 报 `unknown event type`,Rust 报 serde unknown variant),Fold 因此永远只见已知类型——**不 panic,也不静默丢弃**。若业务需要"回滚代码版本后仍能读新事件流",可以显式扩展 wire 层,把未知类型包成不透明的 `PayloadUnknown{type, raw}` 保留、由 Fold 计数跳过(见 §3.5.3)。

### 3.3.3 Schema 版本演进

Payload 的字段变更分三档,每档策略不同:

| 变更              | 例子                                             | 策略                                                                                                                                    |
| ----------------- | ------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------- |
| **加字段**        | `PayloadTurnEnded` 加 `CostUS`                   | 兼容。反序列化把老 Event 的该字段读成零值;Fold 端要能处理零值。                                                                         |
| **删字段**        | `PayloadTurnEnded` 去掉 `LatencyMS`              | 兼容。反序列化时该字段被丢弃;Fold 端不能再依赖它。**旧代码读新 Event 也不受影响**——JSON 里那个字段还在,只是不 map 到任何 struct field。 |
| **改语义/改类型** | `Budget.MaxTokens` 从 `int` 改成 `map[Model]int` | **不兼容**。须新增一个 `PayloadTaskCreatedV2` + 迁移脚本,不允许原地改。                                                                 |

原则一句话:**结构性变更走新的 payload 类型,不原地修改老类型**。老 EventType 保留在事件流里、代码里,直到迁移完成才能清理。

---

## 3.4 EventStore 契约

`EventStore` 是 append-only log。签名极简(ch01 §1.5),但契约不能简。

### 3.4.1 接口

```go
// runtime-go/state/state.go
type EventStore interface {
    Append(events []domain.Event) error
    Load(sessionID string) ([]domain.Event, error)
}
```

```rust
// runtime-rs/src/state/mod.rs
pub trait EventStore {
    fn append(&mut self, events: &[Event]) -> Result<(), StateError>;
    fn load(&self, session_id: &str) -> Result<Vec<Event>, StateError>;
}
```

### 3.4.2 契约条款

- **原子性**:`Append` 传入 N 条,要么 N 条全部落地并可见,要么 0 条落地。**不允许"落了 3 条中途失败"**。多数存储(Postgres 事务、SQLite `BEGIN...COMMIT`、EventStoreDB 的 append batch)天然支持。
- **同 session 串行,跨 session 并行**。`Append` 的临界区**按 session_id 分片**。这也是为什么 §3.3 的 `seq` 是"每 session 单调"而不是"全局单调"——全局单调会把跨 session 的写全部串行化,毫无必要。
- **Load 顺序 = seq 升序**。对同一个 session,任何两次 `Load` 返回的前缀必须完全一致(单调只增,不重排、不丢失)。
- **id 与 ts 由 EventStore 分配**。见 §3.3。调用方递进来的 Event 若填了这两个字段,实现可以覆盖,但应记录 warning。
- **Load 支持从 offset 起**——工程上通常长这样:`Load(session_id, from_seq)`。ch01/ch02 的极简签名没暴露 offset;§3.6 Snapshot 讨论时会引入,视作对现有 Load 的扩展。

### 3.4.3 实现分档

从"能跑"到"生产":

| 档次                 | 实现                                     | 何时用                       |
| -------------------- | ---------------------------------------- | ---------------------------- |
| L1 · 内存            | `Vec<Event>` + Mutex(即 `memfakes`)      | 单元测试、demo、回放本地文件 |
| L2 · 单机文件        | JSONL / SQLite                           | 单机部署、开发环境、边缘设备 |
| L3 · 关系库          | Postgres 单表 + `(session_id, seq)` 主键 | 中等规模、事务性强的业务     |
| L4 · 专用 EventStore | EventStoreDB / Kafka(compacted topic)    | 高吞吐、跨服务共享事件流     |

**换实现不换契约**是本章的第一目标。做到这一点,ch09 Checkpoint 与 ch10 的评测/观测就都不用因为存储换代而重写。

### 3.4.4 与 State 的原子性

ch02 §2.4 明确了协调器内 `Append` 后立刻 `Apply`。体怎么保证:

**方案 A · 同锁**——EventStore 和 State 共用同一把 session 锁,`Append + Apply` 在临界区内成对完成。生产上可合成一个 `SessionLog` 结构体。

**方案 A′ · 协调器内成对、两把锁(教学)**——`memfakes` 与参考 `Runtime` 把 EventStore/State 拆成两个对象、各用一把锁,但 `Step` 内的 `append()` 闭包仍保证**顺序成对**、无中间态。单进程 demo 够用;多 goroutine 并发写同一 Session 时应升级到方案 A。

**方案 B · Outbox + 读复算**——生产环境如果 EventStore 是外部数据库,不好和内存 State 共用锁。这时:

- `Append` 完成后,把新事件塞进本地 outbox 队列。
- Apply 在**每次 `View`** 之前"读齐"到最新 seq。
- 好处:Append 快;坏处:`View` 触发 Apply,首次 View 可能慢。

**方案 C · 主动订阅**——生产级 EventStore 支持 subscribe,State 后台跟随。Apply 与 Append 完全解耦,靠 subscribe 保证最终一致。**但这时 ch02 那条"Append 后立刻 Apply"就是软保证了**——`Step` 前置检查(§2.4 里 `LastTurn[taskID].ID != turnID` 的判断)必须换成"至少读到 seq X"的显式等待。

**本书基线**:用方案 A,因为它把不变量硬性化。方案 B/C 在 ch09 展开,并列出各自需要新增哪些不变量证明。

---

## 3.5 State 与 Fold

State 提供两件事:`Apply` 把 Event 折进 View,`View` 返回只读快照。

### 3.5.1 SessionView 只放"下游需要"的东西

`memfakes.State.applyOne` 按 Event 类型增量更新 SessionView,分两层:

| 层次 | 处理的 EventType | 写入 SessionView 的字段 | 第一部分用途 |
|------|------------------|-------------------------|-------------|
| **生命周期** | `SessionOpened / TaskCreated / TaskEnded / TurnStarted / TurnEnded` | `Session`、`Tasks`、`LastTurn` | ch02 `Step` 前置检查、成本汇总 |
| **Context 投影(占位)** | `ContextCompressed / ProgressUpdated / MemoryQueried` | `WorkingSet`、`Summaries`、`Progresses`、`MemoryRefs` | ch04–ch05 展开;ch02 demo 已预 Fold,供后续章节测试 |

**不进入 SessionView 的消息类 Event**(`UserSpoke / LLMReplied / ToolReturned` 等)留在 EventStore 里,由 Project 阶段**只读**展开成 `Context.Messages`——这不是第二套状态源,而是对已提交事实的展开。

**Project 的合法输入(承 ADR-002)**:

1. **必须先读 `State.View`**,用 SessionView 判断 Task/Turn 是否就绪、预算是否有效。
2. **可以只读 `EventStore.Load`**,按 `task_id` 过滤后展开消息原文(ch02 `memfakes` 平铺全量;ch04 `LayeredContextEngine` 按 WorkingSet 有界展开)。
3. **禁止**用 EventStore 绕过 Fold 做生命周期判断,也禁止在 Assemble 里发外部请求。

这条切分让 SessionView 保持小(每个 Session 通常几百字节到几 KB),`View()` 可以频繁调用。ch04 会展开六层 Context 与 WorkingSet 裁剪策略。

### 3.5.2 Fold 的类型签名

概念上 Fold 是一个纯函数:

```
fold : (SessionView, Event) -> SessionView
```

`State.Apply(events)` = `events.fold(current_view, fold)`。这不是数学玩具——它保证:

1. **重放确定性**:同一 Event 流折叠出同一 View(§3.2 C2)。
2. **可从 Snapshot 继续**:`fold(snapshot_view, new_events) = fold(zero, all_events)`。这是 §3.6 快照能加速恢复的前提。
3. **单元测试极便**:构造几个 Event,断言 View 里对应字段——不用起进程、不用连数据库。

### 3.5.3 处理未知 EventType

遇到一个不认识的 EventType(通常是版本不匹配),两条路:

- **拒绝**(基线):wire 层反序列化直接报错(§3.3.2),事件根本进不了 Fold——§2.7 里 Fold 段"拒绝服务"的策略由此生效。适合"事件流严格由本代码版本产生"的封闭系统。
- **跳过**:wire 层把未知类型保留为不透明 payload(`PayloadUnknown{type, raw}`),Fold 撞到时记一条 warning metric、继续折叠剩下的事件。适合"新版事件流被回滚到旧代码"的场景。**采用时必须给 skip 计数配报警**——跳过是"允许暂时不认识",不是"允许一直不认识"。

选哪条是**部署策略问题**,不是**协议问题**。本书基线走"拒绝",因为它把版本不匹配暴露得最早;"跳过"路径参考实现未包含,需要的团队按上面两步显式扩展。

### 3.5.4 apply 里的"不变量校验"

Apply 单条 Event 前,建议校验的不变量(违反 = event stream 已损坏,应立即拒绝):

- `event.session_id` 非空且与 State 所属的 session 一致。
- `event.seq` 严格大于当前 View 已折叠的最大 seq。
- 若 `event.caused_by` 非空,该 id 必须已在此前的 Fold 中见过。

一次 apply 里做这些检查是**便宜的**(全是内存操作),换来的是**"事件流损坏时立即失败,而不是安静地折出一个错误的 View"**。ch09 讨论 Checkpoint 时会再引用这条——快照本身也要能被校验。

---

## 3.6 Snapshot 与 Turn 边界

Session 跑久了,事件流会很长。**Snapshot 是加速恢复的手段,不是替代事件流**。

### 3.6.1 Snapshot 是什么

一个二元组:

```
Snapshot { seq: int64, view: SessionView }
```

含义:"折叠到 `seq` 为止的 View 长这样。要重建到当前,从 `seq+1` 开始 replay 即可。"

### 3.6.2 拍 Snapshot 的时机:Turn 边界

**关键决策**:Snapshot 只在**Turn 边界**上拍——即刚追加完 `TurnEnded` 之后。

理由:

1. **语义完整**。Turn 中间的 View 可能是"半成品"(LLM 已回复但工具还没跑完);Turn 边界是"内部一致"的自然点。
2. **量级合适**。生产上典型的 Session 每分钟 1-5 个 Turn,每 Turn 拍一次快照 = 每分钟 1-5 次持久化。恢复时从"最近 1 分钟内的快照" replay 几十条 Event,毫秒级。
3. **与 ch09 Checkpoint 对齐**。ch09 会把"Turn 边界快照"升级成"可跨机器恢复的 Checkpoint",两者是同一件事的两个粒度。

**不该做**:每次 Event 都拍快照——写放大严重;定时(每 5 分钟)拍快照——Snapshot 落到 Turn 中间,恢复出的 View 处于半成品状态。

### 3.6.3 恢复流程

Runtime 启动时,对每个 session:

```
1. latest = SnapshotStore.Latest(session_id)   // (seq, view) 或 nil
2. view   = latest?.view ?? SessionView::zero
3. events = EventStore.Load(session_id, from_seq: (latest?.seq ?? 0) + 1)
4. State.ApplyToView(view, events)
5. 把 view 放进 State
```

如果 Snapshot 版本落后于代码(view 的 struct 字段变了),丢弃 Snapshot,从零 Fold ——快照永远是**加速器**,失去它只是慢一点,不影响正确性。

### 3.6.4 SnapshotStore 接口

```go
type SnapshotStore interface {
    Latest(sessionID string) (Snapshot, bool, error)
    Save(sessionID string, snap Snapshot) error
}
```

```rust
pub trait SnapshotStore {
    fn latest(&self, session_id: &str) -> Result<Option<Snapshot>, StateError>;
    fn save(&mut self, session_id: &str, snap: Snapshot) -> Result<(), StateError>;
}
```

**存储实现**:大多数场景与 EventStore 同库(Postgres 单独一张 `snapshots` 表);高并发场景可以走单独的 KV(Redis / DynamoDB)以避免抢占 Event 表的写锁。

---

## 3.7 参考实现

Go / Rust 两份参考实现字段逐一对齐。这一节只列关键代码,完整源码在 `runtime-go/state/` 与 `runtime-rs/src/state/mod.rs`。

### 3.7.1 Wire format(JSON)

**Go**:`json.RawMessage` + factory 表。

```go
// runtime-go/state/wire.go
func UnmarshalEvent(data []byte) (domain.Event, error) {
    var dto EventDTO
    if err := json.Unmarshal(data, &dto); err != nil {
        return domain.Event{}, err
    }
    factory, ok := payloadFactory[dto.Type]
    if !ok {
        return domain.Event{}, fmt.Errorf("unknown event type: %s", dto.Type)
    }
    payload := factory()
    if err := json.Unmarshal(dto.Payload, payload); err != nil {
        return domain.Event{}, err
    }
    return domain.Event{
        ID: dto.ID, SessionID: dto.SessionID, TaskID: dto.TaskID, TurnID: dto.TurnID,
        Type: dto.Type, Payload: derefPayload(payload), TS: dto.TS, CausedBy: dto.CausedBy,
    }, nil
}
```

**Rust**:`serde` adjacently tagged enum 直接给出。

```rust
// runtime-rs/src/state/wire.rs
#[derive(Serialize, Deserialize)]
pub struct EventWire {
    pub id: String,
    pub session_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub task_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub turn_id: String,
    pub seq: i64,
    pub ts: String,          // RFC3339
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub caused_by: String,
    #[serde(flatten)]
    pub payload: EventPayloadWire,   // #[serde(tag = "type", content = "payload")]
}
```

**共同**:两版都在 unit test 里跑一份"序列化后再反序列化,结果与原 Event `Eq`"的往返测试。新增 EventType 时,`type` 字段值先固定,再补 fixture,再改 factory / enum variant——顺序颠倒容易出兼容性事故。

### 3.7.2 内存 EventStore 的完整版

`memfakes.EventStore` 已给出雏形。生产化改造要点:

1. **按 session 分锁**——Mutex 从"整体一把"改成 `map[string]*sync.Mutex`,减少不同 session 的锁竞争。
2. **`seq` 每 session 单调**——从"全局 nextID"改成 `map[string]int64`。
3. **id 用 ULID**——替代 `"e" + counter`;ULID 天然按时间排序,便于跨 session 排查。
4. **`Load(from_seq)`**——添加一个新方法,供 Snapshot 恢复用。老 `Load` 保留(`Load(sessionID) = Load(sessionID, 0)`)。

Go 版签名:

```go
type EventStore interface {
    Append(events []domain.Event) error
    Load(sessionID string) ([]domain.Event, error)
    LoadFrom(sessionID string, fromSeq int64) ([]domain.Event, error)  // §3.6
}
```

Rust 版对齐:

```rust
pub trait EventStore {
    fn append(&mut self, events: &[Event]) -> Result<(), StateError>;
    fn load(&self, session_id: &str) -> Result<Vec<Event>, StateError>;
    fn load_from(&self, session_id: &str, from_seq: i64) -> Result<Vec<Event>, StateError>;
}
```

### 3.7.3 State 的 Apply 骨架

`memfakes.State.applyOne` 已经是完整雏形(见 ch02 引用)。生产版仅新增三条断言(§3.5.4)和一条 seq 校验:

```go
func (s *State) Apply(events []domain.Event) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    for _, e := range events {
        v := s.getOrInit(e.SessionID)
        if err := s.checkInvariants(v, e); err != nil {
            return err
        }
        applyOne(v, e)
        v.MaxSeq = e.Seq
    }
    return nil
}

func (s *State) checkInvariants(v *domain.SessionView, e domain.Event) error {
    if e.SessionID == "" {
        return errors.New("event.session_id is empty")
    }
    if e.Seq <= v.MaxSeq {
        return fmt.Errorf("event seq %d not strictly greater than view maxSeq %d", e.Seq, v.MaxSeq)
    }
    if e.CausedBy != "" && !v.SeenIDs[e.CausedBy] {
        return fmt.Errorf("event %s references unknown caused_by=%s", e.ID, e.CausedBy)
    }
    return nil
}
```

### 3.7.4 Snapshot 端到端 demo

第 2 章那份 20 条 Event 的样本在这一章会以更"真实"的方式再跑一遍:

```bash
# Go: 追加 20 条 → 每次 TurnEnded 拍 snapshot → 关掉进程 → 重启 → 从 snapshot+events 恢复出同样的 SessionView
cd runtime-go && go test ./state -run TestSnapshotReplay -v

# Rust: 同一份场景,同样的断言
cd runtime-rs && cargo test snapshot_replay
```

两版测试都断言四件事:

1. 恢复后的 `SessionView` 与"从零 Fold 全部 20 条"逐字段相等。
2. 恢复只 replay 了 1 条 Event(e20=TaskEnded——最后一次 snapshot 停在 e19=TurnEnded/r3,它之后只追加了这一条)。
3. 未知 EventType 走"拒绝"策略(§3.5.3 基线):构造一条 `type=FutureEvent` 的 JSON,wire 层反序列化如实报错,不 panic、不静默丢弃。
4. `event.seq` 逆序或重复:立即拒绝,`State.Apply` 返回错误。

---

## 3.8 失败模型

呼应 ch02 §2.7 的 Fold 一栏,这里给出**Fold + 存储层**的完整失败矩阵:

| 情形                       | 症状                    | 策略                                                                      |
| -------------------------- | ----------------------- | ------------------------------------------------------------------------- |
| Append 一半失败            | 存储端事务失败          | 存储层保证 all-or-nothing;上层看到 `error`,不 Apply,让 Loop 重试或告警    |
| Apply 抛错(不变量违反)     | 事件流已损坏            | State 拒绝该批 Event;`TaskEnded{failed, reason="fold: ..."}`;需要人工介入 |
| 未知 EventType             | wire 层反序列化报错     | 基线:拒绝(unknown type 错误,事件进不了 Fold);需容忍版本回滚时,显式扩展为"PayloadUnknown 保留 + Fold 跳过 + metric"(§3.5.3) |
| Snapshot schema 与代码不符 | Snapshot 反序列化失败   | 丢弃 Snapshot,从零 Fold(慢一次)                                           |
| 因果链断裂                 | `caused_by` 指向陌生 id | Apply 拒绝——事件流已损坏                                                  |
| Seq 逆序                   | `seq[i+1] <= seq[i]`    | Apply 拒绝——存储层单调性被破坏                                            |
| Wall clock 回退            | `ts` 非单调             | 记录 metric,继续(排序不依赖 wall clock,见 §3.3.1)                         |

**核心哲学(承接 ADR-002)**:**存储层保证原子,Fold 层保证一致,业务层保证补救**。三层各干各的,不越界。

---

## 3.9 取舍记录

| 决策                    | 选择                                    | 代价                                  | 什么情况下会被推翻                                                                                                           |
| ----------------------- | --------------------------------------- | ------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 排序键                  | 每 session 一个 `seq` int64             | 跨 session 排序需要额外机制           | 引入跨 session 因果(如多 session 协作 Agent),会引入全局 Lamport 时钟——但**优先在应用层加显式 causal edge**,不轻易改 seq 定义 |
| id / ts 分配方          | 由 EventStore 分配                      | 客户端不能自证"我发的事件时间是准的"  | 若必须支持客户端离线记录(边缘 Agent 断网时),再引入 `client_ts` 作为附加元数据字段,主排序仍用 EventStore 分配的 `seq`         |
| Payload 版本演进        | 结构性变更走新 EventType,不原地改老类型 | 事件流里会长期携带多版 payload        | 若某类 payload 变化极快(工具协议),给该子集单独用 `PayloadDynamic{kind, body}` 逃生舱,同 ch01 §1.9                            |
| 未知 EventType          | 基线拒绝(wire 层报错),"跳过"留作显式扩展 | 回滚代码版本后,旧代码读不了新事件流,须先升级再回放 | 若部署上频繁出现"新事件流 + 旧代码"(灰度/快速回滚常态化),把"PayloadUnknown 保留 + Fold 跳过 + skip 报警"扩展为基线              |
| Snapshot 时机           | 只在 Turn 边界                          | Session 里 Turn 稀疏时快照更新慢      | 引入长流式工具(ch09)后,Snapshot 也需要在"submit/resume"两点上落——彼时新增 ADR                                                |
| SessionView 的粒度      | 只放"下游判断需要的"最小字段            | Project/Compile 需要额外读 EventStore | 若 Project 变成热路径瓶颈,考虑给 ContextEngine 建独立的 projection(ch04),而**不是**膨胀 SessionView                          |
| Append + Apply 的原子性 | 方案 A(同锁);方案 B/C 留待 ch09         | 单锁限制了跨 session 之外的并行度     | 单机吞吐撞到锁上限,升级到方案 B(Outbox);跨机部署,再升级到方案 C(subscribe)                                                   |
| Fold 的失败处理         | 立即拒绝,不猜、不修复                   | 事件流损坏时 Session 不可用           | Fold 层不做"try to recover"——那是运维决策,不是 Runtime 决策。人工介入或重放降级版事件流                                      |

---

## 3.10 小结

- Event 是"最小的、不可变的、可回放的事实",每条 Event 携带 `session/task/turn` 三层归属和 `caused_by` 因果链,由 EventStore 分配 `id/seq/ts`。
- EventStore 是 append-only log,契约:**原子追加、同 session 串行、Load 顺序 = seq 升序**;实现从内存到 EventStoreDB 分四档,契约不换。
- State = Fold 的结果。Fold 是纯函数,`(SessionView, Event) → SessionView`;SessionView 只放下游判断需要的最小字段,不做"事件流的镜像"。
- Snapshot 在 Turn 边界拍,只做加速器——丢了 Snapshot 从零 Fold 也必须能重建正确的 View。
- 失败模型三层各自兜底:**存储保证原子,Fold 保证一致,业务负责补救**。
- 参考实现:Go/Rust 各一份,序列化走"tag + content",新增 EventType 时靠编译器/factory 表卡住兼容性。

下一章 **第 4 章 · 上下文引擎** 会把 Fold 出的 SessionView + 原始 Event 流,投影成"这次 Turn 要发给 LLM 的上下文",并展开裁剪、压缩、多级降级的设计。

---

## 参考

- [ADR-001 · Runtime 的边界与职责](../adr/ADR-001-runtime-domain.md)
- [ADR-002 · Runtime 数据流协议](../adr/ADR-002-dataflow-protocol.md)——§Decision 里 Fold 的"纯度"定义
- 参考实现:
    - Go: [`runtime-go/state/state.go`](../runtime-go/state/state.go)、[`runtime-go/state/wire.go`](../runtime-go/state/wire.go)、[`runtime-go/runtime/memfakes/memfakes.go`](../runtime-go/runtime/memfakes/memfakes.go)
    - Rust: [`runtime-rs/src/state/mod.rs`](../runtime-rs/src/state/mod.rs)、[`runtime-rs/src/state/wire.rs`](../runtime-rs/src/state/wire.rs)
- 相关章节:`ch01-runtime-domain.md`、`ch02-runtime-dataflow.md`、`ch04-context-engine.md`、`ch09-checkpoint.md`、`ch10-eval.md`
- Martin Fowler, _Event Sourcing_ (2005)
- Greg Young, _CQRS Documents_ (2010)——State 只读视图与 Command 分离的经典阐述
- Leslie Lamport, _Time, Clocks, and the Ordering of Events in a Distributed System_ (1978)——§3.3.1 逻辑时钟的理论基础
- EventStoreDB, _Snapshotting_ 文档——§3.6 的工程化参考
