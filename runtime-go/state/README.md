# runtime-go/state

参考实现：状态与事件存储。

对应章节：`chapters/ch04-state.md`。

计划内容：

- Session / Task / Turn / Event 的数据结构；
- Event Sourcing 与快照；
- 内存实现与可插拔的持久化后端（JSON / SQLite / Postgres）；
- 恢复与回放。

代码将在 Part II 落地。
