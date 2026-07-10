# Chapter 4 · Context Engine

> ch01 定义了世界(Session/Task/Turn/Event),ch02 定义了数据流(Fold/Project/Compile/Chat/Emit),ch03 落地了 State 与 Event 存储。**这一章把"Project"这一段撕开:一次 Turn 到底怎么从几万条 Event 里,拼出一份 LLM 真能用的 Context**——不爆窗口、不烧钱、不丢关键事实、不阻塞热路径。

---

## 4.1 问题:玩具会在 300 turns 上死掉

ch02 §2.8 那个 20 条 Event 的场景,`memfakes.ContextEngine.Assemble` 长这样:把 `UserSpoke`、`LLMReplied`、`ToolReturned` 全部平铺进 Messages。20 条时看起来很干净。

把同一个引擎放进一个真实的 Session——比如一个连续跑三天的运维 Agent、一个陪用户订完机票又订酒店又对齐日程的助理、一个执行 800 步长任务的机器人——**同一段代码会在下面 7 个地方一次性砸下来**:

1. **窗口爆了**。第 137 turn,平铺后 Messages 累计到 210K tokens,模型直接拒绝。业务代码里没有任何地方能拦下这次调用。
2. **成本爆了**。假设窗口够,`210K tokens × $0.003 per 1K = $0.63` 一次调用。一天 500 次 = $315/天/user。CFO 找上门。
3. **首字节延迟到秒级**。`EventStore.Load` 拉 40K 条 Event、Fold 折叠、拼消息——即使全在内存,单次也要 800ms+。用户按下回车到听到 TTS,3 秒起步。
4. **Lost in the middle**。第 89 turn 的用户澄清"其实是发给 Alice 不是 Bob",在长上下文中间被 LLM 忽略。第 200 turn Agent 把邮件继续发给 Bob。业务爆炸,原因隐蔽。
5. **相关性丢失**。当前 Task 是"发邮件",Assemble 却把三小时前"查天气"的完整来回都塞进去了——LLM 得自己找相关线索。相当于每次都让它在一本书里找一页。
6. **工具描述与消息挤成一锅**。`Tool.Schema` 塞进 system 之后,又跟历史消息平铺——LLM 分不清"这段是权威指令"还是"历史片段"。工具调用错乱率明显上升。
7. **压缩过就无法回放**。假设某个团队实现了"每 30 turn 起一个新 Session"来续命——结果生产 bug 复现时,前 30 turn 的关键 tool result 已经"人为忘记",ch01 §1.1 那句"回放"的能力被自己毁了。

**这 7 件事都不是模型能力问题。**都是 ContextEngine 的设计问题。

Runtime 里所有跟"给 LLM 装什么"有关的决策,都归 ContextEngine。这一章就是把这些决策**从 memfakes 平铺里抠出来,变成一个可推理的组件**。

---

## 4.2 概念:六层输入 → 一个 Context

先把 Context 拆开。ch01 定义的 `Context { Messages, Tools }` 是**出口**。入口是**六层输入**,每一层生命周期不同、来源不同、更新频率不同:

```
┌──────────────────────────────────────────────────────────────────┐
│ 1. Instructions        (system prompt / role card / policy)       │← 每 Session 变一次
├──────────────────────────────────────────────────────────────────┤
│ 2. Task Frame          (Task.Goal + Task.Budget + constraints)    │← 每 Task 变一次
├──────────────────────────────────────────────────────────────────┤
│ 3. Working Set         (最近 N 个 Turn 的原文消息)                 │← 每 Turn 变一次
├──────────────────────────────────────────────────────────────────┤
│ 4. Compressed History  (更早 Turns 的结构化摘要)                   │← Compressor 触发
├──────────────────────────────────────────────────────────────────┤
│ 5. Memory Refs         (向量库/KV 检索出的相关片段)                │← 按查询触发 (ch05)
├──────────────────────────────────────────────────────────────────┤
│ 6. Tool Specs          (可用工具的 JSON Schema)                    │← 每 Session/Task
└──────────────────────────────────────────────────────────────────┘
                              ↓ Project
                    Context { Messages, Tools }
                              ↓ Compile (ch04 §4.8)
                              Messages
                              ↓ Chat
                            LLM
```

**读法**——每一层为什么单独存在:

- **Instructions**:是"你是谁"。基本不变,理想情况下应该有 Prompt Caching(见 §4.8)。
- **Task Frame**:是"这次要干什么"。每个 Task 一份;Task 结束就换。这层让 LLM 明确"当前上下文的目的",而不是从 500 条历史里猜。
- **Working Set**:是"刚刚发生了什么"。原文,不摘要,不失真——保护"最近对话"这段最敏感的信号。
- **Compressed History**:是"以前发生过什么"。结构化摘要,可 diff、可回放、可 selective pluck。§4.6 展开。
- **Memory Refs**:是"跟当前问题相关的、跨 Session 的知识"。ch05 展开;ch04 只定义接口。
- **Tool Specs**:是"你能用什么"。Schema,不是自然语言描述。

**关键约束(承 ADR-002)**:Project 是**纯函数**。所以下面这三条不能违反:

1. Assemble 不能在里面调 LLM 做摘要
2. Assemble 不能在里面查向量库
3. Assemble 不能读时钟做决策

**这些"要 IO 的事情",要么在 Project **之前**发生(变成一条 Event 落到 EventStore,再被 Fold 进来),要么在 Compile **之后**发生(Prompt Compiler 内部)。**Project 只是把已经存在的事实拼装成 Context**。§4.4 会展开这条约束的落地。

**六层不是硬性的六个字段**——它们是**六种角色**。同一个引擎实现,可能把 Instructions 和 Task Frame 拼进一条 system message,把 Working Set 摊成多条 user/assistant/tool message。角色是稳定的,messages 的物理形态可以变。

---

## 4.3 Context 生命周期:Hot / Warm / Cold / Archive

用户的 chatgpt.md 里画了一张生命周期图。这里给它工程语义:

```
       New              Hot              Warm              Cold              Archive
       │                 │                 │                 │                  │
   ┌───▼───┐       ┌────▼────┐      ┌─────▼─────┐     ┌────▼─────┐     ┌─────▼──────┐
   │创建   │       │Working  │      │Compressed │     │Memory-   │     │冷存储       │
   │中的   │       │Set 里的 │      │History 里 │     │only(向量)│     │不再进入     │
   │Turn   │       │原文 turn│      │的结构化   │     │不再直接   │     │Prompt      │
   │       │       │         │      │摘要       │     │入 Prompt │     │             │
   └───┬───┘       └────┬────┘      └─────┬─────┘     └────┬─────┘     └─────┬──────┘
       │                 │                 │                 │                  │
     Turn 结束       Working Set        向量化 +          按 Retention        永不删除
     (自动进入      满了(≥ N)        存到 Memory        策略归档            (合规 / 审计)
      Working Set)  → 触发 Compressor    (ch05)
```

**每一层的定义**:

| 层 | 存储 | 进入 Prompt 的方式 | 生命周期触发 |
|---|---|---|---|
| **New** | 内存 (刚 Fold 出来) | 是,作为最近 turn | Turn 结束 → 进 Working Set |
| **Hot** | EventStore | 是,原文 | Working Set 满 (§4.5 触发) → 摘要后转 Warm |
| **Warm** | EventStore + SummaryStore | 是,结构化摘要 | Retention 到 → 转 Cold |
| **Cold** | Memory (向量) | **否**,需 Memory Refs 查询才回到 Prompt | 按业务规则 → Archive |
| **Archive** | 冷存储 (S3 / Parquet) | 否 | 永不(除非合规删除) |

**关键设计选择:迁移是事件驱动的,不是定时的。**

- ❌ 反例:每 5 分钟 cron job 扫一遍,发现"上次访问 > 1 小时的 turn 就摘要"。这套定时策略在生产上永远遇到 corner case——用户刚发完消息就跨过了阈值,摘要与新消息竞态。
- ✅ 正确:**Working Set 大小或 token 达到阈值时,追加一条 `ContextCompressed` Event**。任何压缩都有一条不可变的事实作为凭证。Fold 出来的下一次 SessionView 自然带着新摘要;Assemble 拿到就用。

这条决策直接接住 ch01 §1.1 的**"回放"痛点**:任何 Context 变形都是一条 Event,回放 Event 流 = 复现当时的 Context。

---

## 4.4 Projection Pipeline (纯函数版)

回到 ADR-002:**Project = SessionView → Context,纯函数**。§4.2 说了六层输入,§4.4 展开怎么在纯函数约束下拼出来。

### 4.4.1 数据流

```
EventStore.Load        (from_seq=X)  →  events
                                            ↓ Fold
                                        SessionView + WorkingSet + Summaries + MemoryRefs
                                            ↓ Assemble (纯)
                                        Context
                                            ↓ Compile (纯, §4.8)
                                        Messages
```

**四个"已经在 SessionView 里"的东西**:

1. **SessionView.WorkingSet** — 一个 `[]TurnDigest` (最近 N 个 turn 的原文引用)。由 Fold 维护:每来一条 `TurnEnded` 就把它 append 到 WorkingSet;满了就把最老的踢出。
2. **SessionView.Summaries** — 一个 `map[SummaryID]Summary`。由 Fold 维护:每来一条 `ContextCompressed` Event 就 insert/merge。
3. **SessionView.MemoryRefs** — 一个 `[]MemoryRef`。由 Fold 维护:每来一条 `MemoryQueried` Event 就 append。
4. **SessionView.ToolBindings** — 当前 Task 允许的工具集合。由 Fold 从 `ToolBound` Event 组装。

**Assemble 做的事**——只是"读 SessionView,组装 Context,不做任何 IO":

```
Assemble(sid, tid) :=
    view       := State.View(sid)
    instr      := Instructions(view.Session, view.Tasks[tid])
    frame      := TaskFrame(view.Tasks[tid])
    working    := view.WorkingSet
    summaries  := selectRelevantSummaries(view.Summaries, tid)   // 纯 map 过滤
    refs       := view.MemoryRefs
    tools      := view.ToolBindings

    return Context{
        SessionID: sid, TaskID: tid,
        Messages: layout(instr, frame, working, summaries, refs),
        Tools:    tools.Specs(),
    }
```

`layout` 是纯的:输入相同则输出相同。**没有一次 IO,没有一次 LLM 调用,没有一次时间戳读取**。

### 4.4.2 反例:在 Assemble 里"顺手"做点副作用

生产上第一次遇到"上下文太长"的团队,往往这样改:

```go
// ❌ 反例:在 Project 里"顺手"起个摘要 LLM 调用
func (e *ContextEngineWrong) Assemble(ctx context.Context, sid, tid string) (domain.Context, error) {
    events, _ := e.Store.Load(sid)
    if tokensOf(events) > 30000 {
        summary := callLLMToSummarize(events[:80])  // ⚠️ Project 里发外部请求
        events = append([]Event{ /* fake event with summary */ }, events[80:]...)
    }
    // ...
}
```

demo 里能跑,压测下面三种场景一起来:

- **ch09 Checkpoint 回放**——重放 Event 流时,`callLLMToSummarize` 又被调用了一次,产生**新的**摘要(不同 seed 或不同 provider 版本)。每次回放的 Context 都不一样——回放没有意义了。
- **并发压测**——同一 Session 并发两次 Assemble,同时打了两次摘要 LLM,浪费一次调用;更糟的是两个 goroutine 各拿到不同摘要,后写入的覆盖先写入的,SessionView 一致性崩了。
- **Provider 切换**——把 OpenAI 换成 Anthropic,不仅 Chat 的行为要平移,连 Assemble 内部的摘要行为也要跟着测——ContextEngine 变成了"隐式绑定 Provider"的黑盒。

**正确做法**:摘要是一个独立组件 **Compressor**,由上层 Loop 触发,把结果作为 `ContextCompressed` Event 追加。Assemble 只读事实。§4.5 展开。

### 4.4.3 selectRelevantSummaries 的纯度

Assemble 里那句 `selectRelevantSummaries(view.Summaries, tid)` 看起来很朴素,但**"什么算相关"是纯逻辑**——比如"summary.task_id == tid" + "summary.covers_seq_range 与当前 WorkingSet 无重叠"。**不允许**:

- 用 embedding 相似度过滤(这是 IO)
- 用当前墙钟时间做衰减(这是不确定性)
- 询问 LLM "这条摘要跟当前 Task 相关吗"(这是 LLM 调用)

如果确实需要 embedding 过滤,把它做成 **Memory Refs 层**(§4.2 第 5 层),由 ch05 的 `MemoryQuery` Event 触发,结果作为 `MemoryQueried` Event 落回 SessionView。**Project 依然是纯函数**。

---

## 4.5 Compressor:独立于 Step 的 GC

**Compressor 不在 ch02 §2.4 的 5 段协议里**——它像 GC,独立、可选、有副作用。

### 4.5.1 接口

```go
// runtime-go/compressor/compressor.go(ch04 Round 2 落地)
type Compressor interface {
    // Tick 检查当前 Session 需不需要压缩;需要就产出 ContextCompressed Event。
    // 返回 nil 表示"不需要压缩";返回 []Event 表示"追加这些"。
    Tick(ctx context.Context, sid string) ([]domain.Event, error)
}
```

**上层 Loop 的使用姿势**:

```go
for {
    // 正常 Turn
    rt.Step(ctx, sid, tid, turnID)

    // 每 Turn 结束尝试压缩一次
    if events, _ := compressor.Tick(ctx, sid); len(events) > 0 {
        rt.EventStore.Append(events)
        rt.State.Apply(events)
    }
}
```

**为什么放在 Loop 而不是 Step**——四条理由,每一条都对应一个反例:

- **不阻塞热路径**:摘要 LLM 调用可能几秒钟。放 Step 里 = 用户按回车就等几秒。
- **可选**:轻量场景根本不需要 Compressor。放 Step 里 = 强迫所有场景都装。
- **可换策略**:同一个 Runtime 可以支持"按 turn 数触发""按 token 触发""按 Task 边界触发"多种 Compressor 并存。放 Step 里 = 硬编码一种策略。
- **可以异步**:压缩其实不需要同步等——上一个 Task 已经结束,慢半拍摘要也没关系。放 Step 里 = 同步阻塞。

### 4.5.2 触发时机

四种典型时机,可以并存:

| 时机 | 判定 | 好处 | 代价 |
|---|---|---|---|
| **按 Turn 数** | `len(WorkingSet) ≥ N`(默认 8) | 最简单,可预测 | 与实际 token 量脱钩 |
| **按 Token** | `estimateTokens(WorkingSet) ≥ T`(如 6000) | 与模型窗口对齐 | 估算不准会漏触发 |
| **按 Task 边界** | `TaskEnded` 到来时,把整个 Task 摘要一次 | 语义完整 | 单次任务里救不了长上下文 |
| **按显式请求** | 上层 API `Compressor.Force(sid)` | 应急、测试 | 需要业务判断 |

**基线策略**:按 Turn 数 + 按 Task 边界。按 Token 需要 tokenizer(每个 Provider 都不一样),等 §4.8 Prompt Compiler 引入后再启用。

### 4.5.3 触发的 Event 语义

Compressor.Tick 产出的 Event 序列(顺序不能颠倒):

```
1. MemoryQueried?         (如果同时把老 turn 存进 Memory,先记 query)
2. ContextCompressed      (核心事件:说明"压缩了哪一段 → 生成了什么摘要")
```

`PayloadContextCompressed` 的字段(ch01/ch03 已定义,这里说使用):

```go
type PayloadContextCompressed struct {
    FromSeq   int64      // 覆盖的 seq 范围
    ToSeq     int64
    Strategy  string     // "turns:8" | "task-end" | "manual" | "fallback:flat"
    Summary   Summary    // 结构化摘要,见 §4.6
    FromTokens int64     // 压缩前估算 token(如果 tokenizer 可用)
    ToTokens   int64     // 摘要后 token
}
```

**回放性保证**——`ContextCompressed` 是不可变事实。回放 Event 流时:

- Fold 看到 `ContextCompressed`,把 Summary insert 到 `view.Summaries`。
- WorkingSet 里,`FromSeq..ToSeq` 范围内的 TurnDigest 被 mark 为 `superseded=true`。
- Assemble 拼消息时,superseded 的原文不出现,取而代之的是 Summary。

**结果**:同一份 Event 流,不管什么时候回放,拼出来的 Context 完全相同——因为 Compressor 不再介入,历史事实里已经写死了摘要内容。这是 ADR-002 "回放"能力在 ch04 的兑现。

### 4.5.4 反例:在 Assemble 遇到窗口不够时实时摘要

```go
// ❌ 反例
func (e *ContextEngineWrong2) Assemble(...) (Context, error) {
    ctx := buildContext(...)
    if tokensOf(ctx) > windowLimit {
        ctx = compressOnTheFly(ctx)  // 触发 LLM 摘要
    }
    return ctx, nil
}
```

问题:

- 阻塞热路径(见上)
- Assemble 变有副作用(见 §4.4.2)
- 摘要结果**没有落到 Event 流**——下次 Assemble 又要重摘一次

**正确姿势**:Assemble 只读事实。如果发现"没有对应的 Summary 但 Working Set 太长",两种应对:

1. **快路径**:返回一个降级 Context(§4.9)——比如丢掉最老几个 turn 的原文,先让 Turn 跑起来
2. **慢路径**:上层 Loop 感知到 Assemble 返回 `ContextTooLarge` 时,先跑一次 `Compressor.Force(sid)`,再重试 Assemble

**永远不在 Project 里做 IO**。

---

## 4.6 Summary 策略:结构化,不是自然语言

用户 chatgpt.md 里问过:"为什么 Summary 能压缩?能否设计一种可回放的摘要?"

答案分两半。**能压缩**是因为 Working Set 里有大量"过程性废料"(客套、试探、重复自证)——LLM 的天然冗余。**可回放**要靠**结构化 Summary**——不是自然语言段落,是**约束好的 JSON**。

### 4.6.1 Summary Schema

```go
// runtime-go/summary/summary.go(ch04 Round 2 落地)
type Summary struct {
    // 覆盖范围
    SessionID string
    TaskID    string
    FromSeq   int64
    ToSeq     int64

    // 事实层
    UserIntents    []string          // 用户在这段内表达过的目标
    ToolResults    map[string]any    // key = "tool_name:key_arg";value = 关键返回值
    DecisionsMade  []Decision        // Agent 已做的选择
    OpenQuestions  []string          // 尚未回答的问题
    NextActions    []string          // 计划中的动作(如有)

    // 元数据
    ModelUsed      string            // 生成本摘要用的模型
    PromptVersion  string            // 生成本摘要用的 Prompt 版本
    Confidence     float64           // 生成器自评,0-1
}

type Decision struct {
    What   string  // "选择走 A 方案而不是 B"
    Why    string  // "因为用户明确说 Alice 不吃辣"
    AtSeq  int64   // 决策发生的 Event seq,用于回溯到原文
}
```

**为什么这几个字段**——每个都对应一个具体的"下游可 pluck 场景":

- `UserIntents`:下次 Turn 组装 Context 时,可以把它拼进 Task Frame,让 LLM 时刻知道"用户想要什么"。
- `ToolResults`:避免同一个工具被反复调用(比如查同一个天气三次)。
- `DecisionsMade`:防止 Agent 反复重新讨论同一件事——"上次已经决定了走 A,继续"。
- `OpenQuestions`:让下一个 Turn 优先处理未闭合的分支。
- `NextActions`:多 Turn 计划的骨架,避免 Agent 每次都从 0 想。

**为什么带 `AtSeq`**:任何一条决策/事实,都能沿 `caused_by` 链回溯到原文 Event(ch01 §1.4.1)。**摘要不是遗忘,是索引**。

**为什么带 `ModelUsed` / `PromptVersion`**:摘要本身是"用 LLM 生成的",不同版本会有不同偏差。这些元数据让下游知道"这份摘要来自哪个版本",出问题时能定位。

### 4.6.2 生成 Summary 的 Prompt(反例导向)

**反例 1:自然语言摘要**

```
User: "以下是最近 5 个 Turn 的对话,请用一段话总结。"
Assistant: "用户希望帮忙订机票和酒店。查询了北京到上海的航班……"
```

问题:

- 无法 diff(下次摘要一变,不知道变了什么)
- 无法 selective pluck(下游想只用"tool results"不能)
- 关键 tool result(比如"航班号 CA1509,起飞 08:30")容易被 LLM 意译丢失

**反例 2:一次全量重摘**

```python
# 每次 Compressor 触发,把整段 turns 从头喂给 LLM
prompt = f"Summarize these {N} turns: ..."
```

问题:

- 摘要不稳定——同样输入,不同调用可能生成不同 Summary。
- 昂贵——N 大时反复烧 token。
- 无法增量——新的一批 turns 来了,还要把老 turns 也重摘一遍。

**正确做法:结构化 + 增量合并**

```
User: [System Prompt]
你是 Agent Runtime 的摘要器。请把下面 K 个 Turn 折叠成 JSON,遵循 schema:
{
  "user_intents": [...],
  "tool_results": {...},
  "decisions_made": [{"what": "...", "why": "...", "at_seq": ...}],
  "open_questions": [...],
  "next_actions": [...]
}
规则:
1. tool_results 只保留最新的、非只读的、有 side effect 的
2. decisions_made 必须带 at_seq
3. 有已存在的 Summary,新事实要 merge 到里面(见 <prior_summary>)
4. 用原文关键词,不改写

<prior_summary>
{...上一次的 Summary,如果有...}
</prior_summary>

<new_turns>
[Turn K+1..K+N 的原文]
</new_turns>
```

约束设计的关键:

- **Schema 驱动**——LLM 输出必须匹配 JSON schema。Provider 端用 function calling 或 JSON mode 强制。
- **明确的 merge 语义**——"prior_summary + new_turns → new_summary",增量而非全量。
- **禁止改写**——摘要允许省略,不允许改词。省略靠 confidence 打分事后回捞,改写靠回放才能发现——代价高得多。

### 4.6.3 增量合并规则

每次 Compressor.Tick 触发,`new Summary = merge(prior Summary, new turns)`。合并规则:

| 字段 | 合并规则 |
|---|---|
| `UserIntents` | 追加 + 去重(相似度阈值靠 embedding,如没有则字符串去重) |
| `ToolResults` | 后写覆盖 —— `map[key]` key 冲突时用新的(反映最新事实) |
| `DecisionsMade` | 追加 —— 决策序列有意义,不去重 |
| `OpenQuestions` | 追加;下游可以在下 Turn 由 LLM 显式 `resolved` 后从新 Summary 里删除 |
| `NextActions` | 用新的替换旧的 —— 计划会变 |
| `FromSeq` | 取 min |
| `ToSeq` | 取 max |
| `Confidence` | 取 min —— 一次低置信度污染整段 |

Merge 是纯函数,可以在 Fold 里执行,不需要再调 LLM。**这是"结构化"的最大红利**。

### 4.6.4 什么是坏摘要

在生产上会真实碰到、必须能检出的坏模式:

- **过泛化**:`"用户询问了一些天气问题"` — 具体是哪个城市、哪一天,丢了。
- **动词模糊**:`"处理了邮件发送"` — 发了没?给谁?什么内容?
- **虚构细节**:LLM 在缺信息时会瞎编。要求 `at_seq` 可以强迫它引用原文,减少虚构。
- **过度改写**:`"用户表达了对助手的满意"` — 原话是"还行",别升华。
- **丢 tool result**:天气 API 返回 `{temp: 26}`,摘要里写"天气不错"——下次 Turn 就没数字用了。

**Compressor 里应该有 self-check**:Summary 生成后,跑一次校验(Schema OK / at_seq 都能在 EventStore 找到 / ToolResults 的 key 都存在于原始事件流)。校验失败降级(见 §4.9)。

---

## 4.7 任务进度压缩:Progress 是 Graph,不是百分比

用户列的第二个痛点:**"一天 1000 steps,Prompt 塞不下,怎么办。"**

这是 §4.5-4.6 之外的一个独立压缩维度。§4.6 压缩的是"对话历史",§4.7 压缩的是"任务执行轨迹"。

### 4.7.1 反例:百分比进度

工程界的第一直觉:

```
Task: 做晚饭
Progress: 60%
```

**60% 是什么意思**?

- 已完成的 step 数 / 总 step 数?—— 但总 step 数是变的(遇到问题会加子步骤)
- 时间过半?—— 与任务完成度无关
- LLM 觉得?—— 不稳定

**百分比作为 Progress 是错误的**。**Progress 是一张有状态节点的图**。

### 4.7.2 Progress Schema

```go
// runtime-go/domain/progress.go(ch04 Round 2 落地)
type Progress struct {
    Goal   string      // Task.Goal 的拷贝(避免 Assemble 时再读 Task)
    Done   []Step      // 已完成的关键 Step
    Next   []Step      // 计划中的 Step
    Open   []OpenLoop  // 未闭合的子问题
    // 元数据
    Version   int64    // 每次更新递增
    UpdatedAt Event.ID // 最近一次触发更新的 Event
}

type Step struct {
    Intent      string   // "查询北京明天天气"
    Action      string   // "call tool: weather"
    Observation string   // "temp=26 sky=多云"  (短事实,不是原文)
    Cost        float64  // token or dollar (可选)
    Duration    int64    // ms (可选)
    Kind        StepKind // "decision" | "tool_call" | "user_input" | ...
}

type OpenLoop struct {
    Question   string  // "用户还没说邮件正文"
    RaisedAt   Event.ID
    BlockingSteps []int  // Step 下标,表示"这些计划步骤依赖此问题被解答"
}
```

### 4.7.3 哪些 Step 值得留

**核心问题:1000 个 raw step 里,哪些是 Progress 的"关键 Step"?**

规则:

- **有 side effect 的**——发过的邮件、创建的文件、执行的物理动作。留。
- **有 error 的**——工具失败、LLM refuse、超时。留(下游需要知道"上次为什么没成")。
- **是 decision point 的**——LLM 显式做了选择(比如"选 A 不选 B")。留。
- **user_input**——每一条都留(用户信号最贵)。
- **纯 read-only probe**——查了一次天气、看了一下文件、tokencount 探测。**可丢**(需要时可从 EventStore 重放)。
- **循环体的重复步骤**——在 for 循环里连查 20 个订单,只留"批量查了 20 个订单,3 个失败:X/Y/Z"这样的**聚合**,不留每一个。

**为什么"read-only probe 可丢"是安全的**:read-only 不改变外部世界,重跑代价小(还可以走缓存)。**丢的是 Progress 里的 Step,原始 Event 仍然在 EventStore**。回放能力不受影响(§4.5.3)。

### 4.7.4 1000 steps → 一个 Progress 的例子

一个"帮我整理明天的会议 + 发提醒"的任务,raw 事件 1000+ 条,Progress 折叠后:

```json
{
  "goal": "整理明天会议并逐个发邮件提醒",
  "done": [
    {"intent": "查询明天所有会议", "action": "calendar.list", "observation": "12 个会议", "kind": "tool_call"},
    {"intent": "为每个会议决定提醒对象", "kind": "decision",
     "observation": "3 个内部会议已跳过(团队都知道),9 个跨部门会议需要提醒"},
    {"intent": "查询与会人邮箱", "action": "hr.batch_lookup",
     "observation": "9 个会议共 47 人,2 人邮箱缺失", "kind": "tool_call"},
    {"intent": "发邮件", "action": "email.send x 45",
     "observation": "成功 43,失败 2 (Bob 邮箱异常, Alice 拒收)", "kind": "tool_call"}
  ],
  "next": [
    {"intent": "处理 2 封失败的邮件"},
    {"intent": "通知用户 Bob/Alice 邮箱问题"}
  ],
  "open": [
    {"question": "Bob 的备用联系方式是什么", "raised_at": "e874",
     "blocking_steps": [0]},
    {"question": "Alice 拒收是配置问题还是主动屏蔽", "raised_at": "e879",
     "blocking_steps": [0]}
  ],
  "version": 12,
  "updated_at": "e881"
}
```

- Raw events: 1000+ 条
- Progress: 4 done + 2 next + 2 open = **8 个语义节点**
- Tokens: ~500 (原始 raw 估计 60K)

这就是"Progress 压缩"的定义:**把 O(1000) 的 raw events 折叠成 O(10) 的语义节点,同时保留所有回溯钩子(`at_seq`, `raised_at`)**。

### 4.7.5 Progress 与 State Snapshot 的关系

ch03 §3.6 讲了 Snapshot——把 SessionView 定格。**Progress 是 SessionView 的一部分**,自然跟着 Snapshot 一起走。

区别:

| 概念 | 层次 | 内容 | 用途 |
|---|---|---|---|
| **State Snapshot** (ch03) | 底层 | 完整 SessionView 二进制镜像 | 崩溃恢复的加速器 |
| **Progress** (ch04) | 上层 | Task 内部的语义摘要 | 进 Prompt 的"我做到哪了" |

**State Snapshot 保证"能恢复",Progress 保证"LLM 知道自己做到哪了"。**两件事,同源(Event 流),用途不同。

### 4.7.6 Progress 由谁生成

跟 Summary 类似,由 **Compressor** 生成——但触发时机不同:

- **每 K 个 tool_call 后**(如 K=5)刷新一次 Progress
- **user_input 到来时**立刻刷新(用户最想看到你的进度)
- **error 发生时**立刻刷新(下一个 Turn 需要正确的 Progress)

产出的 Event 是 `ProgressUpdated`(新增 EventType,ch04 Round 2 引入),Fold 时把 `view.Tasks[tid].Progress` 替换。

**Progress 更新是幂等的**——同一版本号写两次结果一样。这条纪律让 Compressor 可以放心重跑(比如 Loop 崩了,恢复后又触发一次)。

---

## 4.8 Prompt Compiler:从 Context 到 Messages

ch01 §1.5 定义了 `PromptCompiler.Compile(Context) → Messages`,空着。§4.8 展开。**这是 ch06 独立一章的话题**,ch04 这里只讲"最小可行版本",让 §4.4 的 Assemble 输出真能被 Chat 消费。

### 4.8.1 六层 → Role 映射

```
Instructions        →  role=system  (position 0)
Task Frame          →  role=system  (position 1, 拼在 Instructions 之后 or 独立 message)
Compressed History  →  role=system  (position 2, 作为"背景信息"标记)
Working Set         →  按 turn 展开:
                        user / assistant / tool 交替
Memory Refs         →  role=system  (position N, 明确标记"参考资料")
Tool Specs          →  Messages.Tools 字段(不进 role 序列)
```

**为什么 Instructions 与 Task Frame 都进 system**:这两层是"权威指令",LLM 在训练时对 system 的服从度更高。放 user 就会被当成"用户建议"。

**为什么 Compressed History 也进 system**:它是"事实性总结",不是对话。放 user 会被 LLM 当作最新用户消息。用 XML tag 或明确标签(`<prior_summary>...</prior_summary>`)包起来。

**为什么 Memory Refs 单独一段**:它是外部注入的知识,不是对话历史。混进 assistant/user 会让 LLM 以为"我说过这个"。

### 4.8.2 反例:把工具描述拼进 system prompt

生产上很多"轻量"实现:

```python
system_prompt = f"""
你是 Agent Runtime。你可以使用这些工具:

weather(city, date) - 查询天气
send_email(to, body) - 发邮件

请合理调用。
"""
```

问题:

- **Provider 换了就崩**——OpenAI 支持 `tools` 字段,Anthropic 用 `tools`,Bedrock 用另一种格式。硬编码在 system 里,换 Provider 得改 prompt。
- **LLM 效果差**——通过 `tools` 字段传的 schema,模型有专门训练;文字描述没有。
- **无法结构化输出**——`tools` 字段的调用会返回带 `tool_call_id` 的响应;prompt 里说的"请调用"只能靠 LLM 自己生成 JSON,格式经常出错。

**正确做法**:`Tool Specs` 走 `Messages.Tools` 字段;系统 prompt 里只说"你有可用工具,通过标准接口调用",不列名单。

### 4.8.3 Provider Adapter

`PromptCompiler` 内部按 Provider 分派:

```go
// runtime-go/prompt/compiler.go(ch04 Round 2 落地)
type PromptCompiler struct {
    Provider ProviderKind  // "openai" | "anthropic" | "bedrock" | "reference"
}

func (c PromptCompiler) Compile(ctx domain.Context) (Messages, error) {
    switch c.Provider {
    case "anthropic":
        return c.compileAnthropic(ctx)
    case "openai":
        return c.compileOpenAI(ctx)
    default:
        return c.compileReference(ctx)  // 通用最小实现
    }
}
```

**每个 adapter 只做 Provider 特定的事**:

- system message 的位置(有些 Provider 只允许一条 system,要合并)
- Tool schema 格式(OpenAI 用 JSON Schema,Anthropic 用 input_schema)
- 消息序列的合法性检查(某些 Provider 不允许连续 user)

**Adapter 之上的 Compile 主逻辑保持一致**:六层输入 → 中间表示 → Adapter 序列化。

### 4.8.4 Prompt 版本与测试

呼应 chatgpt.md 里 "Prompt Compiler" 这一段——**Prompt 不该是字符串,该是版本化的资产**:

- 每个 Prompt 模板(Instructions template / TaskFrame template / Summary generation template)有明确版本号
- 版本存在 `PromptStore`,Compile 时读取
- 每次改 prompt 走 code review + eval(ch10 Evaluation 会展开)
- 生成的 Summary 里带 `PromptVersion`(§4.6.1),出问题能定位是哪个版本导致

**这不在 ch04 落地,ch06 展开**。这里只是让读者知道"路线是这样的"。

---

## 4.9 多级降级

呼应 ch02 §2.7,这里给 ContextEngine 的降级表:

| 触发 | 策略 | 落 Event | 是否终止 Turn |
|---|---|---|---|
| Summary schema 校验失败(§4.6.4) | 丢弃该 Summary,回退到该段 turns 平铺 | `ContextCompressed{strategy="fallback:flat", err="..."}` | 否 |
| Compressor.Tick 时 LLM 失败 | 跳过这次压缩,记录 | `CompressionSkipped{reason="llm_error"}`(新 EventType) | 否 |
| 即使摘要后仍超预算 | 丢掉最老 K 个 turn 的原文,只保留 Summary | `ContextCompressed{strategy="drop:oldest", from_seq, to_seq}` | 否 |
| Memory 层不可用 | 跳过 Memory Refs,只用 Summaries + Working Set | `MemoryQueryFailed{reason}`(ch05 引入) | 否 |
| Tool Specs 拉取失败 | 降级到空 tools 列表——LLM 只能自然语言应答 | `ToolBindFailed{reason}` | 否 |
| 全部失败 | 最小 Context:一条 Instructions + 最新 UserSpoke | `ContextCompressed{strategy="fallback:minimal"}` | 否,但强烈建议上层告警 |

**降级哲学**——与 ADR-002 一致:

- **每一次降级都是一条 Event**——事后可审计
- **不承诺质量,承诺可用**——降级后的 Context 允许 LLM 效果变差,但 Runtime 不能挂
- **不猜、不修复原始事实**——Summary 校验失败 = 丢弃这份摘要,而不是"AI 修补一下"

---

## 4.10 参考实现(Round 2 落地)

**这一节列出 Round 2 会引入的代码骨架**,让读者知道下一次交付会拿到什么。

### 4.10.1 Go / Rust 目录结构增量

```
runtime-go/
  context/
    context.go         (已存在,扩展)
    layered.go         (新增: LayeredContextEngine 实现 §4.4)
    working_set.go     (新增: WorkingSet 结构与 Fold hook)
  summary/
    summary.go         (新增: Summary schema §4.6.1)
    merge.go           (新增: merge 规则 §4.6.3)
    store.go           (新增: SummaryStore 接口 + 内存实现)
  compressor/
    compressor.go      (新增: Compressor 接口 §4.5.1)
    by_turns.go        (新增: 按 turn 数触发的 Compressor)
    by_task.go         (新增: Task 边界触发的 Compressor)
  progress/
    progress.go        (新增: Progress schema §4.7.2)

runtime-rs/src/
  context.rs           (扩展)
  layered.rs           (新增,对应 Go 的 layered.go)
  summary.rs           (新增)
  compressor.rs        (新增)
  progress.rs          (新增)
```

### 4.10.2 端到端测试:Compression Cycle

`runtime-go/context/ch04_compression_test.go` + `runtime-rs/tests/ch04_compression_cycle.rs`:

**30 turns 的场景**,期望:

- 前 3 turns:`LayeredContextEngine.Assemble` 拼出 [system, task_frame, user_1, assistant_1, tool_1, user_2, ...] 完整原文
- 第 4 turn 之前:`Compressor.Tick` 检测到 WorkingSet ≥ 3(测试用小阈值),触发 `ContextCompressed` Event
- 第 5 turn 的 Assemble:拼出 [system, task_frame, <prior_summary>...</prior_summary>, user_4, assistant_4, ...]
- 第 15 turn 时:多轮压缩后,SessionView.Summaries 里有多份 Summary,可以合并
- 断言:整个 Session 的 Event 流可回放(重跑 Fold + Assemble → 同样的 Context)

**这份测试也是 ADR-002 "回放"能力在 ch04 的形式化证据**。

---

## 4.11 取舍记录

| 决策 | 选择 | 代价 | 什么情况下会被推翻 |
|---|---|---|---|
| Summary 形式 | 结构化 JSON schema | 需要 LLM 支持 JSON mode 或 function calling;弱模型效果打折 | 若目标 LLM 稳定不支持 JSON mode,退化为"自然语言 + 后处理正则抽取",但只对该子集破例 |
| 压缩触发方 | 独立 Compressor 组件 + 上层 Loop 触发 | Loop 代码要写触发点 | 若引入"Runtime 内建 Auto-Compress"API,加一个 `AutoCompressOption`,不改 `Step` 语义 |
| 压缩策略数 | 基线两种:按 turn 数 + Task 边界 | 按 token 需要 tokenizer 依赖,先不做 | ch08 Executor 引入 tokenizer 后启用"按 token"策略 |
| Progress 与 Summary | 分开建模(两个 schema) | 有些字段重复(user_intents / open_questions) | 若发现两者字段收敛,合成一个 "SessionDigest";但先分开,避免过早抽象 |
| Compressed History 的位置 | 拼在 system 段,用 `<prior_summary>` XML tag 分隔 | 不同 Provider 对 XML 支持不同 | ch06 Prompt Compiler 展开 Provider adapter 时,可能改为消息序列开头单独一条 |
| Assemble 纯度 | 严格纯函数;不允许时钟/随机/IO | ContextEngine 不能"顺便"发外部请求 | 若某类 IO 属于确定性的(如本地缓存查询,毫秒级 + 只读),写成 ADR 破例;主流程不改 |
| Memory Refs | 只定义接口,ch05 落地 | 本章缺一个具体例子 | 无 |
| Progress 更新粒度 | 每 K tool_call / 每 user_input / 每 error | 高频任务里 Progress 变动频繁 | 若发现 Progress 更新成为热点,退化为按 Turn 边界批量更新 |

---

## 4.12 小结

- ContextEngine 的目标不是"把所有历史塞进去",是"**用有限 tokens 让 LLM 做对当前 Turn**"。
- Context = 六层输入(Instructions / Task Frame / Working Set / Compressed History / Memory Refs / Tool Specs)。每层生命周期与来源不同,不该混用。
- 生命周期分五档(New / Hot / Warm / Cold / Archive),迁移**事件驱动**,不定时。
- **Project 是纯函数**——摘要、检索、生成 Progress 都不在 Assemble 里做,而在上游变成 Event。这是 ADR-002 在 ch04 的具体兑现。
- **Compressor 是独立组件**——像 GC,不在 Step 协议里,上层 Loop 触发。产出的 `ContextCompressed` Event 保证回放性。
- **Summary 是结构化 JSON**——不是自然语言。可 diff、可 merge、可 selective pluck,与用户 Structured Output 工作对齐。
- **Progress 是 Graph,不是百分比**——1000 raw steps → O(10) 语义节点,同时保留 `at_seq` 回溯钩子。
- **Prompt 是版本化资产**——从"拼字符串"到"Prompt Compiler",Provider 差异抽象到 Adapter,不外泄。
- 多级降级保证 Runtime 可用性优先于 Context 质量——每次降级都留一条 Event。

下一章 **Chapter 5 · Memory Architecture** 会展开 Memory Refs 这一层:向量库、KV、跨 Session 长期记忆的接入协议。

---

## 参考

- [ADR-001 · Runtime 的边界与职责](../adr/ADR-001-runtime-domain.md)
- [ADR-002 · Runtime 数据流协议](../adr/ADR-002-dataflow-protocol.md)——§Decision 里 Project 的"纯度"定义在本章反复应用
- 参考实现骨架(Round 2 落地):
  - Go: `runtime-go/context/layered.go`、`runtime-go/summary/`、`runtime-go/compressor/`、`runtime-go/progress/`
  - Rust: `runtime-rs/src/layered.rs`、`runtime-rs/src/summary.rs`、`runtime-rs/src/compressor.rs`、`runtime-rs/src/progress.rs`
- 相关章节:`ch01-runtime-domain.md`(六层 Context 与 §1.4 对应)、`ch02-runtime-dataflow.md`(§2.5 纯度约束)、`ch03-state-event.md`(§3.5 SessionView 的最小字段)、`ch05-memory.md`(Memory Refs 展开)、`ch06-prompt-compiler.md`(§4.8 展开)、`ch07-planner.md`(Task Graph 与 Progress 关系)
- 研究/工程参考:
  - Nelson Liu et al., *Lost in the Middle: How Language Models Use Long Contexts* (2023)
  - Anthropic, *Prompt Caching* (2024) —— Instructions 层的成本优化
  - MemGPT: Charles Packer et al. (2023) —— 分层 Context 的经典参考
  - Voyager: Guanzhi Wang et al. (2023) —— Skill 库的启发,ch14 会再讨论
