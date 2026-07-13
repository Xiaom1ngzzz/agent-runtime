# 第 5 章 · 记忆架构

> ch04 定义了 Context 的六层输入,其中第 5 层 `Memory Refs` 挂在那里等着展开。这一章把它撕开:**什么该进 Memory、什么不该;写入怎么写、查询怎么查;跨 Session 的记忆如何在纯函数 Assemble 里被安全消费**。

---

## 5.1 问题:Summary 救不了一切

ch04 §4.5 的 Compressor 解决的是**同一 Session 内的历史压缩**。但真实系统里,还有 4 类信息 Summary 永远救不了:

1. **跨 Session 的用户偏好**。用户上周说过"我不吃辣",这周开新 Session 问菜谱——上周的 Session 早关了,Summary 是 Session 内的,不会传过来。
2. **组织级知识**。公司的 API 手册、runbook、合规文档。**每个 Session 都要用,不属于任何一个 Session**。
3. **长期事实**。Alice 的邮箱、Bob 的入职日期、某个数据库表的 schema。**变化极慢,查询极频繁**。
4. **过去执行的 pattern**。"上次做类似任务时,先查 X 再调 Y 效果好"—— Voyager 那种 skill library 的雏形。

这 4 类共同点:**它们的生命周期超出单个 Session,却需要在多个 Session 的 Prompt 里出现**。ch03 的 EventStore、ch04 的 Compressor 都不合适——前者的锁按 session 分片,后者的输出绑定当前 Session。

**Memory 层就是为这 4 类信息存在的。**

**反例:直接把这些塞进 Instructions**

生产上第一次遇到"用户偏好要记住"的团队,常常这样做:

**Go**

```go
// ❌ 反例
systemPrompt := fmt.Sprintf(
    "You are an agent. User preferences: %s. Company policies: %s. Facts DB: %s.",
    userPrefs, policies, factsJSON,
)
```

**Rust**

```rust
// ❌ 反例
let system_prompt = format!(
    "You are an agent. User preferences: {user_prefs}. Company policies: {policies}. Facts DB: {facts_json}.",
);
```

问题:

- **Instructions 层越滚越大**——每个用户的偏好都往里塞,10 万 tokens 起
- **无法差异化**——不管当前 Task 是什么,所有偏好都进 Prompt。查天气也塞"我不吃辣"
- **无法失效**——用户改了偏好,老 Session 的 Instructions 里还是旧的
- **Prompt Cache 失效**——按 ch04 §4.8 的设计,Instructions 应该是稳定的 cache 命中区。塞变化的东西进去,cache miss

**正确做法**:把这 4 类信息交给 **Memory 层**,通过**检索**在需要时才进 Prompt。

---

## 5.2 概念:Memory 是"事件流的对立面"

对比一下 ch03 的 EventStore 和 ch05 的 Memory,它们看起来都是"存东西",实际上是**两个正交轴**:

| 维度         | EventStore (ch03)   | Memory (ch05)                 |
| ------------ | ------------------- | ----------------------------- |
| **主键**     | `(session_id, seq)` | 内容语义(key、embedding、tag) |
| **写入**     | append-only,不可变  | upsert,允许更新和过期         |
| **读取**     | 按 seq 顺序全量拉   | 按查询召回 Top-K              |
| **归属**     | 单 Session          | 跨 Session                    |
| **生命周期** | 与 Session 同       | 独立,按业务规则               |
| **目的**     | 回放、审计、Fold    | 检索、注入相关知识            |
| **CAP 偏好** | 一致性优先          | 可用性优先(容忍不新鲜)        |

**核心洞察**:EventStore 是"发生过什么的账本",**必须精确**;Memory 是"可能相关的参考",**允许模糊**。这两条哲学决定了它们的接口设计完全不同。

### 5.2.1 Memory 的三层

```
┌──────────────────────────────────────────────────────────────┐
│  Working Memory   (Session 内的临时状态,如 scratchpad)         │← 其实是 Context 的一部分,ch04 讲过
├──────────────────────────────────────────────────────────────┤
│  Episodic Memory  (发生过的事,跨 Session:上周的对话摘要)         │← 由 Compressor 归档过来
├──────────────────────────────────────────────────────────────┤
│  Semantic Memory  (稳定的事实与偏好:用户信息、KB 文档、schema)    │← 由业务或人工写入
└──────────────────────────────────────────────────────────────┘
```

**读法**:

- **Working Memory** 不是 Memory 层的职责。它就是 SessionView 里的 WorkingSet(ch04 §4.4.1),存在于事件流里。ch05 不重复讲。
- **Episodic Memory** 是"事件流的归档"。Compressor 觉得某段 Session 值得永久保留时,把 Summary 写进 Episodic Memory,附上 embedding。下次跨 Session 需要"上次做类似任务时怎么做"时,靠 embedding 检索。
- **Semantic Memory** 是"结构化事实"。用户偏好、公司文档、schema 表——**它们不是事件的产物,是外部注入的**。

**这一章主要讲 Episodic 与 Semantic**。

### 5.2.2 与 ch04 Memory Refs 的关系

ch04 §4.2 第 5 层是 `Memory Refs`。ch05 的 MemoryStore 就是这一层的**后端**。数据流:

```
业务 / Compressor  →  MemoryStore.Upsert    (写入)
                          ↓
                    (存储 + 索引)
                          ↓
Compressor / 上层 Loop → MemoryStore.Query  →  MemoryQueried Event
                                                    ↓
                                              Fold → SessionView.MemoryRefs
                                                    ↓
                                     LayeredContextEngine.Assemble (纯)
```

**关键**:检索(`Query`)**不发生在 Assemble 里**(违反 ADR-002 的纯度约束)。检索发生在上层 Loop 或 Compressor 里,产出 `MemoryQueried` Event,Fold 后 SessionView 就自然带着 `MemoryRefs`。Assemble 只读事实。

这条纪律和 ch04 §4.4.2 反例("在 Assemble 里做 IO")是**同一条**。

---

## 5.3 Memory Item 的形态

Memory 里存的每一条,统称 `MemoryItem`:

**Go**

```go
// runtime-go/domain/memory.go(ch05 Round 2 落地)
type MemoryItem struct {
    // 身份
    ID     string
    Source string      // "user_pref" | "kb.docs" | "session_summary" | ...
    Kind   MemoryKind  // "semantic" | "episodic"

    // 内容
    Key      string     // 语义 key,如 "user:42:diet"
    Content  string     // 文本;也是索引对象
    Metadata map[string]string

    // 索引
    Embedding []float32  // 可空;为空表示只支持 keyword/exact 查询
    Tags      []string

    // 生命周期
    CreatedAt string    // Event ID(如果由 Event 产生)或时间戳
    UpdatedAt string
    ExpiresAt string    // 可空;到期后 Query 不再返回
    Version   int64     // upsert 时递增

    // 溯源
    OriginSession string  // 可空;若是从某 Session 归档过来
    OriginTaskID  string
    OriginSeqFrom int64
    OriginSeqTo   int64

    // 多租户隔离(生产强制)
    TenantID string  // 非空;Query/Upsert 必须带 tenant 过滤,禁止跨 tenant 召回
}
```

**Rust**

```rust
// runtime-rs/src/memory/mod.rs(ch05 Round 2 落地)
pub struct MemoryItem {
    pub id: String,
    pub source: String,
    pub kind: MemoryKind,
    pub key: String,
    pub content: String,
    pub metadata: HashMap<String, String>,
    pub embedding: Vec<f32>,
    pub tags: Vec<String>,
    pub created_at: String,
    pub updated_at: String,
    pub expires_at: String,
    pub version: i64,
    pub origin_session: String,
    pub origin_task_id: String,
    pub origin_seq_from: i64,
    pub origin_seq_to: i64,
    pub tenant_id: String,
}
```

**生产约束 · `TenantID`**:Memory 跨 Session,天然是多租户系统的高风险面。**`MemoryStore.Query` 与 `Upsert` 必须把 `tenant_id` 作为强制过滤字段**(通常来自 `Session.Principal` 或上层租户上下文),禁止"只按 embedding 相似度"全局检索。漏加 tenant 过滤 = 跨租户数据泄漏。Round 2 参考实现未包含多租户,但协议层应预留该字段。

**为什么每一个字段都在**:

- `Kind` 分 `semantic` / `episodic` —— Query 时可以按类型过滤(找"上次执行 pattern"不该混"用户邮箱")
- `Embedding` 可空 —— 允许 keyword-only 的 KV 后端(Redis / DynamoDB),不强制向量库
- `ExpiresAt` —— **Memory 与 EventStore 的关键差异**:允许失效。用户改了偏好,老条目该过期
- `Origin*` —— **溯源钩子**:每一条 Memory 都能追回到"从哪个 Session 的哪段 seq 归档来的",与 ch03 §3.3 的 `caused_by` 精神一致
- `Version` —— upsert 幂等的基础(同版本写两次结果一样)

### 5.3.1 反例:把 EventStore 里的原文直接写进 Memory

看起来是"跨 Session 保留细节",实际问题:

- 数据量爆炸(整个 Session 的原文)
- 检索效果差(检索匹配到 tool_call 的字段名,不是语义)
- 隐私风险(用户敏感对话被向量化后跨 Session 泄漏)

**正确做法**:**只把 Summary 归档到 Memory**,原文留在 EventStore。理由:

- Summary 已经通过 LLM 抽象了细节
- Summary 的 schema 稳定,可控制 Metadata / Embedding 的生成
- 老 Session 需要审计时,原文还在 EventStore 里(通过 `OriginSession` 回溯)

---

## 5.4 MemoryStore 接口

极简契约,类似 ch03 的 EventStore:

**Go**

```go
// runtime-go/memory/memory.go(ch05 Round 2 落地;Query 类型在 runtime-go/domain/memory.go)
type MemoryStore interface {
    // Upsert 插入或更新。若 Key 已存在:比较 Version,新的 > 旧的才生效。
    // 幂等:同 (Key, Version) 写两次结果一致。
    Upsert(ctx context.Context, item MemoryItem) error

    // Query 按查询召回 Top-K。见 5.5。
    Query(ctx context.Context, q Query) ([]MemoryRef, error)

    // Expire 让指定 Key 立即过期(相当于 ExpiresAt = now)。
    Expire(ctx context.Context, key string) error
}

type Query struct {
    // 至少填一个查询条件
    Semantic   string     // 走 embedding
    Keywords   []string   // 走 keyword match
    Tags       []string   // 精确匹配 tag
    KindFilter MemoryKind // 可空
    SourceFilter []string // 可空

    // 结果控制
    TopK           int
    MinScore       float64
    IncludeExpired bool
}
```

**Rust**

```rust
// runtime-rs/src/memory/mod.rs
pub trait MemoryStore {
    fn upsert(&self, item: MemoryItem) -> Result<(), MemoryError>;
    fn query(&self, q: &Query) -> Result<Vec<MemoryRef>, MemoryError>;
    fn expire(&self, key: &str) -> Result<(), MemoryError>;
}

pub struct Query {
    pub semantic: String,
    pub keywords: Vec<String>,
    pub tags: Vec<String>,
    pub kind_filter: Option<MemoryKind>,
    pub source_filter: Vec<String>,
    pub top_k: usize,
    pub min_score: f64,
    pub include_expired: bool,
    pub tenant_id: String,
}
```

### 5.4.1 契约条款

- **Upsert 幂等**:`Upsert(item) + Upsert(item)` = `Upsert(item)`。生产上重试机制的前提。
- **Query 是纯读**:不改变任何状态。**但仍然是 IO**——不能在 Assemble 里调。
- **顺序无关**:Query 的返回顺序按 score 降序,协议不保证同 score 的顺序。L1 内存实现为了测试可复现,额外按 Key 做稳定排序;调用方不得依赖这一细节。
- **过期语义**:`ExpiresAt` 已过的条目**默认不返回**,可通过 `IncludeExpired=true` 强制读(审计用)。
- **不承诺一致性**:Upsert 后立即 Query,可能读不到(索引未刷新)。上层业务不应依赖"upsert 之后一定能查到"。

**这条最后的契约是关键**——Memory 相对于 EventStore,主动放弃了强一致性,换来的是可用性和查询多样性(向量 / keyword / tag)。

### 5.4.2 实现分档

沿用 ch03 §3.4.3 的分档思路:

| 档次        | 实现                                       | 特性                        |
| ----------- | ------------------------------------------ | --------------------------- |
| L1 · 内存   | `map[string]MemoryItem` + 暴力扫 embedding | 单元测试、demo              |
| L2 · 单机   | SQLite + `sqlite-vss` / `sqlite-vec`       | 单机部署、边缘设备          |
| L3 · 关系库 | Postgres + pgvector                        | 中等规模,与 EventStore 同库 |
| L4 · 专用   | Pinecone / Weaviate / Milvus               | 高吞吐、跨服务共享          |

本书 **ch05 Round 2 落地 L1**——内存 fake,足够展示接口与语义;L2+ 是工程细节,不同团队会走不同后端。

---

## 5.5 Retrieval:查什么、怎么查

Query 的三种典型模式,组合使用:

### 5.5.1 模式 1:精确匹配

```
Query{Keywords: ["user:42:diet"], KindFilter: "semantic"}
```

用于**已知 key 直接取**。用户偏好、schema 表这类场景。

**为什么不叫 Get(key)**:因为可能一次要拿多个相关 key,`Keywords` 数组支持"OR 语义"。

### 5.5.2 模式 2:语义相似

```
Query{Semantic: "帮我订机票", TopK: 5, MinScore: 0.7}
```

用于**"跟当前任务相关的历史执行"**。走 embedding。

**关键**:`MinScore` 阈值必须设。否则不管多不相关都返回 K 条,LLM 被垃圾干扰。

### 5.5.3 模式 3:标签过滤

```
Query{Tags: ["domain:travel", "user:42"], TopK: 20}
```

用于**结构化召回**。比如"用户 42 的所有旅行相关记忆"。

### 5.5.4 组合查询

生产上通常混用:

```
Query{
    Semantic:     "帮 alice 订机票",
    Tags:         ["user:42"],           // 只查这个用户的
    KindFilter:   "episodic",             // 只要历史执行
    SourceFilter: ["session_summary"],   // 只要 Session 归档,不要 raw 事件
    TopK:         3,
    MinScore:     0.75,
}
```

`Semantic` 决定粗召回,`Tags` / `KindFilter` / `SourceFilter` 做精筛。

### 5.5.5 反例:一次查询用所有维度

```
Query{TopK: 100, MinScore: 0}
```

问题:

- LLM 上下文被 100 条低质量结果淹没
- 检索延迟高
- Memory Refs 层展开成 100 条 system message,喧宾夺主

**正确做法**:**TopK 保守(3-10),MinScore 严格(>=0.7),尽量多用 Tag / Kind 精筛**。Memory 是"参考",不是"喂全部信息"。

---

## 5.6 Upsert:什么该写、什么不该

### 5.6.1 该写入 Memory 的四类

对应 §5.1 那四类:

1. **用户级偏好** —— 用户显式说过的规则("我不吃辣""邮件必须包含称呼")。**Upsert on user_id + preference_key**
2. **组织级知识** —— 公司 API 手册、runbook、schema。**Upsert on document_id**;通常靠 batch 导入,不是 Runtime 侧写入
3. **长期事实** —— 联系方式、身份信息。**Upsert on entity_type + entity_id + field**
4. **历史执行 pattern** —— Compressor 归档过来的 Session Summary。**Upsert on session_id + task_id**;Kind=episodic

### 5.6.2 不该写入 Memory 的三类

- **原始 Event 内容**(见 §5.3.1 反例)
- **单次 Turn 的中间结果**(那是 Working Memory,存 EventStore 就够了)
- **临时的、马上过期的信息**(如 OTP 验证码、临时 token)—— 用 KV 加 TTL,不用 Memory

### 5.6.3 写入的触发路径

三条典型路径:

**路径 A · Compressor 归档**(自动)

> **实现状态**:本路径是生产协议设计。Round 2 的 `ByTurns` Compressor 不依赖 MemoryStore;§5.10 的测试由上层显式 Upsert 一条 Episodic Memory 来验证同样的数据形态。

```
Compressor.Tick(sid) 检测到 Task 结束
    → 除了追加 ContextCompressed Event
    → 还调 MemoryStore.Upsert(item{Kind=episodic, Source=session_summary, Origin=(sid,tid,seq_range)})
```

**路径 B · 显式用户指令**(半自动)

```
用户说"记住我不吃辣"
    → LLM 判定这是一条 preference,调 tool: remember(key="user:42:diet", value="no_spicy")
    → tool 内部调 MemoryStore.Upsert(item{Kind=semantic, Source=user_pref})
    → 追加 ToolReturned Event(记录"已记住")
```

**路径 C · Batch 导入**(离线)

```
运维:把公司文档批量导入 Memory(pgvector / Pinecone),Runtime 只读
```

**这三条路径的共同点**:Upsert **不发生在 Assemble 里**。§5.2.2 强调过。

---

## 5.7 Retrieval 的时机与协议

回到 §5.2.2 那个数据流:检索发生在**上层 Loop 或 Compressor** 里,产出 **MemoryQueried Event**。

### 5.7.1 何时触发 Retrieval

对齐 ch04 §4.5.2 的时机表:

| 时机                     | 触发方             | 例子                                                                      |
| ------------------------ | ------------------ | ------------------------------------------------------------------------- |
| **Task 开始时**          | 上层 Loop          | 拿到 `Task.Goal` 后,先查"上次做类似任务的方式"                            |
| **User 输入之后**        | 上层 Loop          | 每条 UserSpoke 都触发一次相关记忆检索                                     |
| **Turn 内 LLM 显式请求** | LLM 通过 tool call | LLM 说"帮我查一下 Alice 的邮箱",触发 tool: recall(key="user:alice:email") |
| **Compressor 归档时**    | Compressor         | 归档 Session Summary 后,顺便回查"最近类似 Summary 有没有"                 |

### 5.7.2 MemoryQueried Event 语义

**Go**

```go
type PayloadMemoryQueried struct {
    Query string       // 查询原文(用于回放能精准复现查询)
    Refs  []MemoryRef  // 检索结果的快照
}
```

**Rust**

```rust
pub struct PayloadMemoryQueried {
    pub query: String,
    pub refs: Vec<MemoryRef>,
}
```

**关键**:`Refs` 里存的是**当时查到的结果的完整快照**,不是"记一个 query 让 Assemble 时再查一遍"。

**为什么**:

- Assemble 是纯函数,不能查(§5.2.2)
- 回放:重放事件流时,重新查会因为 Memory 变化拿到不同结果 → 回放失败
- 审计:出问题时能问"当时到底给 LLM 看了哪 3 条 Memory"

**代价**:Event 体积变大。缓解方式:`MemoryRef.Content` 可以只存 excerpt(前 200 字);完整内容通过 `Ref.Source` + `Ref.Key` 追溯到 MemoryStore。

### 5.7.3 反例:实时检索

**Go**

```go
// ❌ 反例
func (e *ContextEngineWrong) Assemble(ctx, sid, tid) (Context, error) {
    view := state.View(sid)
    // 每次 Assemble 都查一次 Memory
    refs, _ := memoryStore.Query(ctx, Query{Semantic: task.Goal})
    return Context{
        MemoryRefs: refs,   // 直接塞进去
    }, nil
}
```

**Rust**

```rust
// ❌ 反例
fn assemble_wrong(&self, sid: &str, tid: &str) -> Result<Context, ContextError> {
    let view = self.state.view(sid)?;
    // 每次 Assemble 都查一次 Memory
    let refs = self.memory.query(&Query {
        semantic: view.tasks[tid].goal.clone(),
        ..Default::default()
    })?;
    Ok(Context {
        memory_refs: refs,
        ..Default::default()
    })
}
```

问题(和 ch04 §4.4.2 完全同构):

- Assemble 变有 IO,违反 ADR-002
- 回放失败(第二次回放时 Memory 变了,Refs 不同)
- 并发压测时 Memory Query 被打爆
- 换 MemoryStore 时行为漂移

**正确做法**:上层 Loop 触发 Query → 追加 `MemoryQueried` Event → Fold 后 SessionView.MemoryRefs 就有了 → Assemble 只是把 Refs 拼进 Prompt。

---

## 5.8 生命周期与失效

Memory 的四类信息,每类有不同的生命周期规则:

| 类型             | 典型 TTL                     | 失效策略                           |
| ---------------- | ---------------------------- | ---------------------------------- |
| **用户偏好**     | 无(直到用户改)               | Upsert 覆盖                        |
| **组织文档**     | 天/周(取决于 batch 更新频率) | 定期 refresh + version bump        |
| **长期事实**     | 无到几个月                   | Version 递增;老 Version 不进 Query |
| **历史 pattern** | 月级                         | ExpiresAt 显式设                   |

### 5.8.1 版本化的 Upsert

Upsert 时比较 Version:

```
Upsert(item{Key: "user:42:diet", Version: 3})
```

- 若已有 Version=2 的 → 覆盖
- 若已有 Version=3 的 → 幂等,忽略
- 若已有 Version=4 的 → 拒绝(不允许倒退)

这条规则让并发 Upsert 安全,也让离线 batch 导入不会覆盖线上更新。

### 5.8.2 过期的两种模式

- **Soft expire**:`ExpiresAt` 到了 → Query 默认不返回,但物理数据保留(审计用)
- **Hard expire**:定期 GC(每天/每周)物理删除过期数据

推荐 Soft expire。因为:

- 保留数据便于审计与回放
- 存储成本可控(embedding 与元数据本身不大)
- 用户"记错了偏好"要恢复时,能查回来

### 5.8.3 反例:让 Memory 也是不可变的

有人会问:为什么不干脆把 Memory 也做成 append-only,和 EventStore 一样?

问题:

- 用户偏好一天改 5 次,`user:42:diet` 存 5 个版本——Query 时要跑聚合逻辑(取最新)
- 存储不断增长,而 90% 是被覆盖的旧版本
- 与 §5.4 的"CAP 偏好可用性"矛盾

**正确边界**:**EventStore 是账本(不可变);Memory 是投影缓存(可变)**。EventStore 里已经有"用户在 seq=N 说过我不吃辣"这条 Event 作为审计凭证——Memory 只是让下次 Query 更快找到,不需要重复不可变性。

---

## 5.9 多级降级

对齐 ch02 §2.7 / ch04 §4.9 的失败模型:

| 触发                      | 策略                                          | Event                                                       | 是否终止 Turn |
| ------------------------- | --------------------------------------------- | ----------------------------------------------------------- | ------------- |
| MemoryStore.Query 超时    | 跳过检索,不注入 Refs                          | `MemoryQueryFailed{reason="timeout"}`(规划中)               | 否            |
| 返回结果 0 条             | 正常,不算失败                                 | `MemoryQueried{Refs: []}`                                   | 否            |
| 返回结果太多(超预算)      | 按 score 取前 K,记录被丢弃的数量              | `MemoryQueried{Refs: [...], dropped=N}`(dropped 字段规划中) | 否            |
| MemoryStore 完全不可用    | Runtime 继续,LayeredContextEngine 跳过第 5 层 | `MemoryQueryFailed{reason="store_down"}`(规划中)            | 否            |
| Upsert 冲突(Version 倒退) | 拒绝写入,记录                                 | `MemoryUpsertRejected{reason="version_regression"}`(规划中) | 否            |

表中标"规划中"的 EventType / 字段是失败模型的完整设计,参考实现(L1 内存档)未包含——它们在接入真实向量库后端时才有意义,届时随 ADR 引入。

L1 内存实现已对 Version 倒退返回可识别错误(且不覆盖新值);`MemoryUpsertRejected` Event 留给接入上层 Loop 时落地。

**核心哲学**:Memory 是"参考",不是"必需"。**Memory 全部挂掉,Runtime 仍能跑**(效果打折,但不 crash)。这与 EventStore 挂掉必然全站停摆(ch03 §3.8)形成鲜明对比。

---

## 5.10 参考实现(Round 2 已落地)

### 5.10.1 目录结构增量

```
runtime-go/
  domain/
    memory.go            (新: MemoryItem/MemoryKind/Query 类型)
  memory/
    memory.go            (新: MemoryStore 接口)
    inmem.go             (新: 内存 fake 实现,L1 档次)

runtime-rs/src/
  memory/
    mod.rs               (新: 对应 Go 的三个文件)
```

### 5.10.2 端到端测试:Memory Cycle

`runtime-go/memory/ch05_memory_cycle_test.go` + `runtime-rs/tests/ch05_memory_cycle.rs`:

**场景**:

1. Batch 导入 3 条 Semantic Memory(用户偏好 ×2 + KB 文档)
2. 归档 1 条 Episodic Memory(Session Summary,带 `Origin*` 溯源字段)
3. 新 Session 的 Task 开始,上层 Loop 触发组合查询(§5.5.4):`Query{Semantic: "帮 alice 订机票", Tags: ["user:42"], SourceFilter: [...]}`
4. 把返回的 Refs 打包成 `MemoryQueried` Event 追加,Fold → `SessionView.MemoryRefs` 非空
5. `LayeredContextEngine.Assemble` 拼出 Context,含 `<memory_ref>` 块
6. 回放性:全量 Fold 后 SessionView.MemoryRefs 完全一致

**断言**:

- Upsert 幂等(同 Key/Version 写两次结果相同)
- Query 按 Score 降序
- Query 尊重 MinScore
- Assemble 输出含 `<memory_ref source=... score=...>` 标签
- 回放 SessionView 一致

---

## 5.11 取舍记录

| 决策                       | 选择                                          | 代价                           | 什么情况下会被推翻                                                                              |
| -------------------------- | --------------------------------------------- | ------------------------------ | ----------------------------------------------------------------------------------------------- |
| Memory 与 EventStore 分开  | 两个独立组件,两套语义(不可变 vs 可变)         | 概念多一个                     | 若发现某场景 Memory 也需要严格不可变 + 回放(如金融合规),把该子集用 EventStore 存,不动整体规则   |
| Memory 是"投影缓存"        | 允许 Upsert / Expire,不承诺强一致             | Upsert 后立即 Query 可能读不到 | 若上层强依赖"写后立即读",引入 read-your-writes 模式作为可选                                     |
| Retrieval 触发在上层 Loop  | 不在 Assemble;不在 Runtime 内建               | 上层要写触发代码               | 若引入统一的 `Task.Prepare(ctx)` 门面,可以把 Memory 触发收进 Runtime                            |
| MemoryQueried Event 存快照 | 存完整 Refs,不存 raw Query                    | Event 体积增大                 | 若发现 Event 太大成为瓶颈,改为存 `RefIDs`,Assemble 时从 MemoryStore 二次拉取(但破坏回放性,慎用) |
| MemoryItem 三层分类        | semantic / episodic / (working 在 EventStore) | 分类不 100% 清晰的边界会有争议 | 若发现某类信息不适合任一 Kind,加新 Kind 而不是复用                                              |
| 版本化 vs 不可变           | 版本化 Upsert                                 | 需要维护 Version 字段          | 若某类 Memory 频繁修改且不需要回滚,给该 Source 关闭 Version 校验                                |
| Tag/Kind/Source 三维过滤   | 三个独立字段                                  | 一次 Query 要考虑三种筛选      | 若发现基本用不上 Tag,把它降级为 Metadata 里的一个字段                                           |
| Embedding 可空             | 允许 keyword-only 的 Memory                   | Query 时要判断 backend 支持    | 若所有生产后端都必需 embedding,收紧为非空                                                       |

---

## 5.12 小结

- Memory 层解决 Summary 救不了的 4 类问题:**跨 Session 偏好、组织知识、长期事实、执行 pattern**。
- Memory 与 EventStore 是**正交的两个轴**:**EventStore 不可变、单 Session、精确;Memory 可变、跨 Session、模糊检索**。
- Memory 分三层:Working(其实是 EventStore 里的 WorkingSet)/ Episodic / Semantic。ch05 主讲后两层。
- **Retrieval 不在 Assemble 里发生**——与 ch04 §4.4.2 的纯度约束一致。查询由上层 Loop 触发,结果作为 `MemoryQueried` Event 落回 SessionView。
- `MemoryItem` 带 `Version`、`ExpiresAt`、`Origin*` 三组字段,支持幂等 upsert、软过期、跨 Session 溯源。
- Query 组合 `Semantic + Tags + Kind + Source`,反例是"一次查全部"。
- 生命周期规则按类型分档;推荐 Soft expire。
- 降级路径:Memory 全部挂掉,Runtime 继续跑(效果打折)。

下一章 **第 6 章 · Prompt 编译器** 会展开 ch04 §4.8 的"从 Context 到 Messages",给出 Provider Adapter 的落地(OpenAI / Anthropic),并把 Prompt 从"字符串"变成"版本化资产"。

---

## 参考

- [ADR-001 · Runtime 边界与职责](../adr/ADR-001-runtime-domain.md)
- [ADR-002 · Runtime 数据流协议](../adr/ADR-002-dataflow-protocol.md)——本章 Retrieval 的纯度约束再次应用
- [ADR-003 · Runtime 与 DDD 对应关系](../adr/ADR-003-ddd-mapping.md)——MemoryStore 是典型的 Repository
- 参考实现(Round 2 已落地):
    - Go: [`runtime-go/memory/memory.go`](../runtime-go/memory/memory.go)、[`runtime-go/memory/inmem.go`](../runtime-go/memory/inmem.go)、[`runtime-go/domain/memory.go`](../runtime-go/domain/memory.go)
    - Rust: [`runtime-rs/src/memory/mod.rs`](../runtime-rs/src/memory/mod.rs)
- 相关章节:`ch03-state-event.md`(§3.4 EventStore 契约对比)、`ch04-context-engine.md`(§4.2 六层输入的第 5 层,§4.5 Compressor 触发)、`ch06-prompt-compiler.md`(Memory Refs 的展开渲染)
- 研究/工程参考:
    - MemGPT: Charles Packer et al. (2023) —— Episodic Memory 的分层设计源头
    - Voyager: Guanzhi Wang et al. (2023) —— Skill Library 是"执行 pattern" Memory 的经典形态
    - Mem0: Kiran Kolli et al. (2024) —— 生产级 Memory 层的开源实现
    - pgvector, Pinecone, Weaviate, Milvus —— 生产存储后端
