# 第 9 章 · 检查点与恢复

> ch03 把 Snapshot 定义成 Turn 边界加速器。这一章把它升级为 **Checkpoint**:带 schema 版本、深拷贝含 Context 字段、一条 `Recover` 恢复路径。内存参考实现验证恢复算法;持久化 wire 格式仍是生产扩展。

---

## 9.1 问题:Snapshot 还差三步

1. **深拷贝漏字段**。早期 `cloneSnap` 只拷贝 Tasks/LastTurn/SeenIDs,WorkingSet/Progresses/Summaries 丢了——恢复后 Context 层空白。
2. **没有 schema 版本**。代码升级后旧快照反序列化"碰巧成功但语义错",比直接失败更危险。
3. **恢复流程散落在测试里**。需要一条库函数:`Recover(session, checkpointStore, eventStore, state)`。

副作用回放(ch02 §2.7)原则不变:**`Recover` 本身只 Fold,不重跑工具**。不过 ch08 的一次性 Executor 仍存在"外部成功、结果未落库"窗口;Checkpoint 不能独自提供 exactly-once,生产上需幂等键或 outbox。

**`Recover` 要求传入 fresh/空的 State 实例**——不得在已有 Session 视图上叠加恢复;协调器应先 `NewState()` 或显式 `Reset(sessionID)`,再调用 `Recover`。

---

## 9.2 概念:Checkpoint = Snapshot + 元数据

```
Checkpoint {
  SchemaVersion: 1,
  Snapshot: { Seq, View }
}
```

恢复:

```
1. cp = CheckpointStore.Latest(sid)
2. if cp.ok && version match → State.LoadSnapshot(cp.View); from = cp.Seq
   else → from = 0
3. events = EventStore.LoadFrom(sid, from)
4. State.Apply(events)
```

---

## 9.3 设计要点

### 9.3.1 深拷贝

`cloneSnap` 必须覆盖 ch04 字段:`WorkingSet`、`Summaries`、`MemoryRefs`、`Progresses`,并隔离其中的嵌套 slice/map。**深拷贝边界**:Checkpoint 保存的是 `SessionView` 投影,不含 EventStore 原文;恢复后 Project 仍须从 EventStore 按 WorkingSet 展开消息。内存 Store 在 Save 和 Latest 两侧都返回独立副本。

### 9.3.2 Schema 不匹配

丢弃 Checkpoint,全量 replay。慢一次,正确性优先。

### 9.3.3 Append+Apply 方案 B/C

ch03 §3.4.4 的 Outbox / Subscribe 仍标为扩展。Round 2 基线保持方案 A(协调器内成对)。Checkpoint 不依赖 B/C。

### 9.3.4 跨机器

跨进程恢复还需要稳定的 Checkpoint wire DTO、校验和与原子写入。Round 2 只用内存 Store 证明"版本匹配则增量 replay、版本不匹配则全量 replay"的算法;ch03 的 Event wire JSON 不能直接等同于 Checkpoint 格式。

---

## 9.4 API

**Go**

```go
type CheckpointStore interface {
    Latest(sessionID string) (Checkpoint, bool, error)
    Save(sessionID string, cp Checkpoint) error
}

func TakeCheckpoint(sessionID string, st State, cps CheckpointStore) error
func Recover(sessionID string, cps CheckpointStore, store EventStore, st RecoverableState) (replayed int, err error)
```

**Rust**

```rust
pub trait CheckpointStore {
    fn latest(&self, session_id: &str) -> Result<Option<Checkpoint>, StateError>;
    fn save(&mut self, session_id: &str, cp: Checkpoint) -> Result<(), StateError>;
}

pub fn take_checkpoint(
    session_id: &str,
    state: &dyn State,
    cps: &mut dyn CheckpointStore,
) -> Result<(), StateError>;

pub fn recover(
    session_id: &str,
    cps: &dyn CheckpointStore,
    store: &dyn EventStore,
    state: &mut dyn RecoverableState,
) -> Result<usize, StateError>;
```

两端内存 Store 都保存完整 `Checkpoint`,不会在读回时把旧版本伪装成当前版本。

---

## 9.5 测试

```bash
cd runtime-go && go test ./state -run TestCh09 -v
cd runtime-rs && cargo test ch09_checkpoint
```

断言:Turn 边界拍照 → 追加 TaskEnded → Recover 只 replay 1 条 → Progress/WorkingSet 仍在;克隆不被调用方污染;schema 不匹配时退回全量 replay。

`TakeCheckpoint` 本身不检查最后一条 Event 是否为 `TurnEnded`;"只在 Turn 边界调用"是当前协调层约定,不是 API 已强制的不变量。

---

## 9.6 取舍记录

| 决策 | 选择 | 代价 | 什么情况下会被推翻 |
|------|------|------|----------|
| Checkpoint 时机 | 调用方约定在 Turn 边界 | API 不验证边界,长工具中间不可恢复 | 协调器校验或 submit/resume 点另拍 |
| schema 策略 | 不匹配则丢弃 | 大 Session 恢复变慢 | 引入迁移函数 |
| 副作用 | 不重放工具 | 外部世界与 Event 不一致时需人工 | 幂等工具 + outbox |

---

## 9.7 小结

- Checkpoint 让 Snapshot 在参考实现中可版本化、可一键恢复;跨进程 wire 格式仍待定义。
- 深拷贝补齐 Context 字段后,恢复视图与全量 Fold 一致。
- 下一章 **第 10 章 · 评测与优化** 用评测框架把"跑通"变成"可回归"。

---

## 参考

- Go: [`runtime-go/state/checkpoint.go`](../runtime-go/state/checkpoint.go)
- Rust: [`runtime-rs/src/state/checkpoint.rs`](../runtime-rs/src/state/checkpoint.rs)
- 恢复图:[`diagrams/ch09-checkpoint-recovery.mmd`](../diagrams/ch09-checkpoint-recovery.mmd)
- 相关:`ch03` §3.6、`ch08`
