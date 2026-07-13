# ADR-004: Task Graph 与 Saga / Process Manager

- **状态**: Accepted
- **日期**: 2026-07-13
- **决策者**: —
- **上游**: [ADR-003 · Runtime 与 DDD 对应关系](ADR-003-ddd-mapping.md)
- **落地章节**: ch07-planner.md

## Context

ADR-003 明确暂缓 Saga / Process Manager,触发条件是"ch07 引入 sub-Task"。ch07 现已需要:

- 父 Task 派生子 Task;
- 子 Task 成败汇合到父 Task;
- 取消/预算沿父子关系继承(Round 2 先做预算均分)。

需要一次书面决策:用什么模式协调,以及边界在哪。

## Decision

1. **Task 通过 `ParentID` 形成树**(Round 2 不做任意 DAG 边)。图由 `SessionView.Tasks` 派生,不单独存边表。
2. **Planner** 负责产出 `SubTaskSpawned` / `TaskCreated{ParentID}` / `ProgressUpdated`,是 Domain Service。
3. **SagaCoordinator** 扮演 Process Manager:观察子 Task 终态,追加父 `TaskEnded`。Round 2 只做汇合,不做补偿事务。
4. **预算继承**:子 Task 均分父 `MaxTokens`;其它 Budget 字段拷贝。级联取消留扩展。

## Consequences

### 正向

- 取消/评测可以对准子 Goal;
- Progress 有明确写入方;
- 与 ADR-003 表格中"何时引入 Saga"的触发条件闭合。

### 负向

- 无补偿:已产生副作用的子 Task 失败时,需业务层自行处理;
- `" + "` 拆分启发式过简,真实系统要换 LLM Planner。

## Alternatives

**A. 继续扁平 Task,靠 LLM 多 Turn 自规划** —— 拒绝:无法做子级取消与预算。

**B. 完整工作流引擎(BPMN/Temporal)** —— 过重;Runtime 边界内用最小 Saga 即可。

**C. 任意 DAG + 显式边 Event** —— 留待多父依赖出现时再开 ADR。

## References

- ch07-planner.md
- ADR-003 §"未采用的 DDD 部分 · Saga"
- Hector Garcia-Molina et al., *Sagas* (1987)
