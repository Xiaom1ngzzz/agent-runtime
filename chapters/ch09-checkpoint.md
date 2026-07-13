# 第 9 章 · 检查点与恢复

> ch03 把 Snapshot 定义成 Turn 边界加速器。这一章把它升级为 **Checkpoint**:带 schema 版本、深拷贝含 Context 字段、一条 `Recover` 恢复路径——让"进程挂了再起来"成为测试里可跑的事实。

---

## 9.1 问题:Snapshot 还差三步

1. **深拷贝漏字段**。早期 `cloneSnap` 只拷贝 Tasks/LastTurn/SeenIDs,WorkingSet/Progresses/Summaries 丢了——恢复后 Context 层空白。
2. **没有 schema 版本**。代码升级后旧快照反序列化"碰巧成功但语义错",比直接失败更危险。
3. **恢复流程散落在测试里**。需要一条库函数:`Recover(session, checkpointStore, eventStore, state)`。

副作用回放(ch02 §2.7)原则不变:**恢复只 Fold,不重跑工具**。工具结果已在 Event 流里。

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

`cloneSnap` 必须覆盖 ch04 字段:`WorkingSet`、`Summaries`、`MemoryRefs`、`Progresses`。

### 9.3.2 Schema 不匹配

丢弃 Checkpoint,全量 replay。慢一次,正确性优先。

### 9.3.3 Append+Apply 方案 B/C

ch03 §3.4.4 的 Outbox / Subscribe 仍标为扩展。Round 2 基线保持方案 A(协调器内成对)。Checkpoint 不依赖 B/C。

### 9.3.4 跨机器

`Checkpoint` + 可序列化 Event 流即可迁移到另一进程。Round 2 用内存 Store 证明逻辑;落盘格式复用 ch03 wire JSON。

---

## 9.4 API

```go
type CheckpointStore interface {
    Latest(sessionID string) (Checkpoint, bool, error)
    Save(sessionID string, cp Checkpoint) error
}

func TakeCheckpoint(sessionID string, st State, cps CheckpointStore) error
func Recover(sessionID string, cps CheckpointStore, store EventStore, st RecoverableState) (replayed int, err error)
```

Rust 对齐:`take_checkpoint` / `recover`。

---

## 9.5 测试

```bash
cd runtime-go && go test ./state -run TestCh09 -v
cd runtime-rs && cargo test ch09_checkpoint
```

断言:Turn 边界拍照 → 追加 TaskEnded → Recover 只 replay 1 条 → Progress/WorkingSet 仍在;克隆不被调用方污染。

---

## 9.6 取舍记录

| 决策 | 选择 | 代价 | 推翻条件 |
|------|------|------|----------|
| Checkpoint 时机 | 仍在 Turn 边界 | 长工具中间不可恢复 | submit/resume 点另拍 |
| schema 策略 | 不匹配则丢弃 | 大 Session 恢复变慢 | 引入迁移函数 |
| 副作用 | 不重放工具 | 外部世界与 Event 不一致时需人工 | 幂等工具 + outbox |

---

## 9.7 小结

- Checkpoint 让 Snapshot 可版本化、可一键恢复。
- 深拷贝补齐 Context 字段后,恢复视图与全量 Fold 一致。
- 下一章 **第 10 章 · 评测与优化** 用评测框架把"跑通"变成"可回归"。

---

## 参考

- Go: [`runtime-go/state/checkpoint.go`](../runtime-go/state/checkpoint.go)
- Rust: [`runtime-rs/src/state/checkpoint.rs`](../runtime-rs/src/state/checkpoint.rs)
- 相关:`ch03` §3.6、`ch08`
