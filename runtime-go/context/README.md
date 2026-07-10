# runtime-go/context

上下文系统的 Project 层参考实现。

对应章节：

- `chapters/ch04-context-engine.md`：六层 Context、WorkingSet、Summary、Progress
- `chapters/ch05-memory.md`：`MemoryQueried` Fold 后的 Memory Refs 渲染
- `chapters/ch06-prompt-compiler.md`：Project / Compile 边界

主要文件：

- `context.go`：`ContextEngine` 接口
- `layered.go`：将 `SessionView` 与 EventStore 消息原文投影成布局后的 `Context.Messages`

相关组件与验证：

- `runtime-go/compressor/`：结构化摘要与压缩事件
- `runtime-go/memory/`：跨 Session Memory
- `runtime-go/prompt/`：类型检查与 Provider Emit
- `go test ./compressor ./memory ./prompt`：Part II 可执行测试
