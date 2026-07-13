# 第 7 章 · 规划器与任务图

> 第一、二部分把"一次 Turn 怎么跑"讲清楚了。生产 Agent 的目标很少是单步的——"查天气 + 发邮件"天然是两件事。这一章把扁平 Task 打开成 **Task Graph**,并引入 **Planner** 与最小 **Saga** 协调。

---

## 7.1 问题:一个 Goal 装不下两件事

ch01 §1.9 把 Task 按扁平处理。demo 里 Goal 写 `"查天气 + 发邮件"`,LLM 在多个 Turn 里自己拆步——能跑。规模上来之后会撞这几面墙:

1. **取消粒度不对**。用户说"邮件别发了",你只能取消整个 Task,天气查询也被砍掉。
2. **预算无法拆分**。父 Goal 有 `MaxTokens=8000`,两个子目标抢同一池子,没有继承规则。
3. **Progress 靠猜**。ch04 已经有 Progress schema,但谁在何时写出 `ProgressUpdated`?没有 Planner,就只能手写。
4. **并行机会浪费**。查天气和起草邮件本可并行,扁平 Task 只能串行 Turn。

**需要一张图,而不是一串 Turn。**

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

`PayloadTaskCreated` 同步增加 `ParentID`。旧事件缺字段 → 反序列化为空串 → 根 Task,兼容。

### 7.3.2 `SubTaskSpawned`

Planner 的意图事件。Round 2 里 `GraphPlanner` 同时产出 `SubTaskSpawned` + `TaskCreated{ParentID}`,便于审计"谁决定拆分"。

### 7.3.3 `BuildTaskGraph`

```go
g := domain.BuildTaskGraph(view.Tasks)
g.Roots              // ParentID == ""
g.ChildrenOf("t1")   // 直接子节点
```

---

## 7.4 规划器接口

```go
type Planner interface {
    Plan(ctx context.Context, view domain.SessionView, taskID string) ([]domain.Event, error)
}
```

**契约**:

1. Planner **只读** View,返回待 Append 的 Event——不碰 EventStore。
2. 幂等优先:已有子 Task 时不再重复 spawn。
3. 预算继承:子 Task 均分父 `MaxTokens`(Round 2 简化规则)。

**Go 参考实现** `planner.GraphPlanner`:Goal 含 `" + "` 则拆分子目标;否则尝试 `ProgressUpdated`。

**Rust** 对齐:`planner::GraphPlanner`。

---

## 7.5 Saga:子成败如何汇到父

```go
ended, _ := planner.SagaCoordinator{}.OnChildEnded(view, parentID)
// 全部 Succeeded → TaskEnded{Succeeded}
// 任一 Failed/Canceled/Timeout → TaskEnded{Failed}
// 仍有 Running → 空
```

> **实现状态**:Round 2 只做"全成/有败"两种汇合。补偿(撤销已发邮件)、超时级联取消留给扩展。

---

## 7.6 Progress 自动生成

ch04 §4.7.6 把触发器留到本章。Round 2 策略:

- 尚无 Progress → 生成一版;
- 已有子图 → 按子 Task 状态填 `Done` / `Next`。

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

断言:拆出 2 子 Task、ParentID/预算正确、Saga 关父、Progress.Done=2。

---

## 7.8 取舍记录

| 决策 | 选择 | 代价 | 推翻条件 |
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
- Go: [`runtime-go/planner/`](../runtime-go/planner/)
- Rust: [`runtime-rs/src/planner/`](../runtime-rs/src/planner/)
- 相关:`ch01`(扁平 Task)、`ch04`(Progress)、`ch08`(Executor)
