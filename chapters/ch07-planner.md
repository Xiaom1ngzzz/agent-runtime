# 第 7 章 · 规划器与任务图

> 第一、二部分把"一次 Turn 怎么跑"讲清楚了。生产 Agent 的目标很少是单步的——"查天气 + 发邮件"天然是两件事。这一章把扁平 Task 打开成 **Task Graph**,并引入 **Planner** 与最小 **Saga** 协调。

> **写法上的变化**:第一、二部分为建立方法论,反复用"反例 → 后果 → 正确做法"和逐章的失败退化表展开。方法论已经立住,第三、四部分(ch07–ch10)转为更紧凑的协议规格与取舍记录,不再重复同一套演示——这是有意的收紧,呼应各章"实现状态"标注里对当前落地边界的诚实说明,而不是内容被裁剪。

---

## 7.1 问题:一个 Goal 装不下两件事

ch01 §1.9 把 Task 按扁平处理。demo 里 Goal 写 `"查天气 + 发邮件"`,LLM 在多个 Turn 里自己拆步——能跑。规模上来之后会撞这几面墙:

1. **取消粒度不对**。用户说"邮件别发了",你只能取消整个 Task,天气查询也被砍掉。
2. **预算无法拆分**。父 Goal 有 `MaxTokens=8000`,两个子目标抢同一池子,没有继承规则。
3. **Progress 靠猜**。ch04 已经有 Progress schema,但谁在何时写出 `ProgressUpdated`?没有 Planner,就只能手写。
4. **并行机会浪费**。查天气和起草邮件本可并行,扁平 Task 只能串行 Turn。

**需要一张图,而不是一串 Turn。**

本章先建立表达与汇合能力;细粒度取消 API、级联规则和子 Task 并行调度尚未实现。

---

## 7.2 概念:任务图 + 规划器 + Saga

```
Session
 └── Task(t1, goal="查天气 + 发邮件")     ← 父 / 根
      ├── Task(t1.s1, goal="查天气")      ← 子
      └── Task(t1.s2, goal="发邮件")      ← 子
```

三个角色:

| 角色 | 职责 | 不做什么 |
|------|------|----------|
| **规划器 (Planner)** | 读 `SessionView`,决定是否派生子 Task、是否更新 Progress | 不调 LLM、不跑工具 |
| **任务图 (Task Graph)** | 从 `Tasks` 派生的只读图(`Roots` / `Children`) | 不单独持久化 |
| **SagaCoordinator** | 子 Task 全部终态后关闭父 Task | 不做补偿事务(Round 2) |

与 DDD 的对应见 [ADR-004](../adr/ADR-004-task-graph-saga.md):Saga / Process Manager 在这里落地。

---

## 7.3 领域扩展

### 7.3.1 `Task.ParentID`

**Go**

```go
// runtime-go/domain/domain.go
type Task struct {
    ID, SessionID, ParentID string // ParentID 空 = 根
    Goal string
    Status TaskStatus
    Budget Budget
    // ...
}
```

**Rust**

```rust
// runtime-rs/src/domain/mod.rs
pub struct Task {
    pub id: String,
    pub session_id: String,
    pub parent_id: String, // 空 = 根 Task
    pub goal: String,
    pub status: TaskStatus,
    pub budget: Budget,
    // ...
}
```

`PayloadTaskCreated` 同步增加 `ParentID`。旧事件缺字段 → 反序列化为空串 → 根 Task,兼容。

### 7.3.2 `SubTaskSpawned`

Planner 的意图事件。Round 2 里 `GraphPlanner` 同时产出 `SubTaskSpawned` + `TaskCreated{ParentID}`,便于审计"谁决定拆分"。参考 Fold 会先为 Spawn 建 pending 占位;若规划 Event 列表只追加了一部分就崩溃,下一次 Plan 会补发缺失的 `TaskCreated` 或尚未创建的兄弟子任务。**注意**:EventStore 要求 `Append` 原子批,但 Planner 可能返回多条 Event——协调器须么一次 `Append` 全部规划 Event,要么在崩溃恢复后由 Planner 幂等补全;**不能假设"部分 append 成功"后 Saga 仍安全**。

### 7.3.3 `BuildTaskGraph`

**Go**

```go
g := domain.BuildTaskGraph(view.Tasks)
g.Roots              // ParentID == ""
g.ChildrenOf("t1")   // 直接子节点
```

**Rust**

```rust
let g = build_task_graph(&view.tasks);
g.roots              // parent_id 为空
g.children_of("t1")  // 直接子节点
```

`Roots` 与 `Children` 都按 Task ID 稳定排序。当前模型没有显式创建序号,因此不能把 map/HashMap 遍历顺序解释为创建顺序。

---

## 7.4 规划器接口

**Go**

```go
type Planner interface {
    Plan(ctx context.Context, view domain.SessionView, taskID string) ([]domain.Event, error)
}
```

**Rust**

```rust
pub trait Planner {
    fn plan(&self, view: &SessionView, task_id: &str) -> Result<Vec<Event>, PlannerError>;
}
```

**契约**:

1. Planner **只读** View,返回待 Append 的 Event——不碰 EventStore。
2. 幂等优先:已有子 Task 时不再重复 spawn。
3. **预算继承**:子 Task 继承父 `Budget` 全字段——`MaxTokens` 按商和余数分配;`MaxCostUS`、`MaxWallMS` 同样按子任务数均分(余数给前几个子任务),总和不超过父预算。父预算小于子任务数时允许部分子任务得到 0。

**Go 参考实现** `planner.GraphPlanner`:Goal 含 `" + "` 则拆分子目标;否则尝试 `ProgressUpdated`。

**Rust** 对齐:`planner::GraphPlanner`。

---

## 7.5 Saga:子成败如何汇到父

**Go**

```go
ended, _ := planner.SagaCoordinator{}.OnChildEnded(view, parentID)
// 全部 Succeeded → TaskEnded{Succeeded}
// 任一 Failed/Canceled/Timeout → TaskEnded{Failed}
// 仍有 Running → 空
```

**Rust**

```rust
let ended = SagaCoordinator.on_child_ended(&view, parent_id)?;
// 全部 Succeeded → TaskEnded{Succeeded}
// 任一 Failed/Canceled/Timeout → TaskEnded{Failed}
// 仍有 Running → 空 Vec
```

> **实现状态**:Round 2 只做"全成/有败"两种汇合。**Saga 仅在 Planner 规划完成(子 `TaskCreated` 全部落库)后生效**;若规划 Event 只追加了一部分就崩溃,恢复后须先由 Planner 补全子 Task,再评估父 Task 终态。补偿(撤销已发邮件)、超时级联取消留给扩展。

---

## 7.6 Progress 自动生成

ch04 §4.7.6 把触发器留到本章。Round 2 策略:

- 尚无 Progress → 生成一版;
- 已有子图 → 按子 Task 状态填 `Done` / `Next`。
- 语义内容未变化 → 不发新 Event,避免仅因重复 Plan 提升 Version。

触发仍由上层 Loop 在 Plan 时机调用,不塞进 `Runtime.Step`。

---

## 7.7 参考实现与测试

```
runtime-go/planner/{planner.go,graph.go,ch07_task_graph_test.go}
runtime-go/domain/task_graph.go
runtime-rs/src/planner/mod.rs
runtime-rs/tests/ch07_task_graph.rs
```

```bash
cd runtime-go && go test ./planner -run TestCh07 -v
cd runtime-rs && cargo test ch07_task_graph
```

断言:拆出 2 子 Task、ParentID/预算正确、子预算不超父预算、Saga 关父、Progress.Done=2,以及相同状态重复 Plan 不追加 Progress。

---

## 7.8 取舍记录

| 决策 | 选择 | 代价 | 什么情况下会被推翻 |
|------|------|------|----------|
| 拆分启发式 | Goal 按 `" + "` 分割 | 真实 Planner 应用 LLM | 引入 LLM Planner 时换策略,接口不变 |
| 图存储 | 派生自 Tasks,不单独表 | 深查询要遍历 map | 图查询成为热路径再物化 |
| Saga | 最小汇合,无补偿 | 副作用回滚靠业务 | 需要分布式事务时扩展 ADR-004 |
| Progress 触发 | Plan 时生成 | Loop 要记得调 Plan | 可挂到 ToolReturned 钩子 |

---

## 7.9 小结

- Task 通过 `ParentID` 形成图;Graph 是 View 上的派生结构。
- Planner 产出规划 Event;Saga 汇合子 Task 终态。
- Progress 由 Planner 维护,接上 ch04 schema。

下一章 **第 8 章 · 执行器** 把工具调度从 memfake 升级为可超时、可绑定失败的执行器。

---

## 参考

- [ADR-004 · 任务图与 Saga](../adr/ADR-004-task-graph-saga.md)
- [ADR-003 · DDD 对应](../adr/ADR-003-ddd-mapping.md)
- Go: [`runtime-go/planner/graph.go`](../runtime-go/planner/graph.go)
- Rust: [`runtime-rs/src/planner/mod.rs`](../runtime-rs/src/planner/mod.rs)
- 结构图:[`diagrams/ch07-task-graph.mmd`](../diagrams/ch07-task-graph.mmd)
- 相关:`ch01`(扁平 Task)、`ch04`(Progress)、`ch08`(Executor)
