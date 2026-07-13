# runtime-go/state

参考实现：状态与事件存储。

对应章节：`chapters/ch03-state-event.md`、`chapters/ch09-checkpoint.md`。

已落地内容：

- `EventStore` / `State` 接口与内存实现；
- JSON wire format（`wire.go`，`ts` 为 RFC3339 字符串）；
- `Snapshot` / `Checkpoint` 与 `Recover`（须传入 fresh State）；
- Fold 不变量校验（seq 单调、`caused_by` 链）。

构建与测试：

```bash
cd runtime-go && go test ./state -v
```

生产扩展（见 `chapters/ch11-production-boundaries.md` 与 ADR-005–009）：持久化后端、租约、幂等 dedup、跨进程 Checkpoint wire。
