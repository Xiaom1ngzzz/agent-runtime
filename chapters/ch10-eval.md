# 第 10 章 · 评测与优化

> 前面九章把 Runtime 建成可跑、可恢复的系统。这一章回答:**怎么知道改 Prompt / Planner / 工具绑定没有把系统弄坏?**最小答案是一个结构化的 **Eval** 框架——对比金标事件流与实际事件流,产出可断言的 Score。

---

## 10.1 问题:没有回归,就没有演进

1. 改一句 Instructions,线上行为漂了,没有本地红灯。
2. "看起来差不多"无法进 CI。
3. Token / 工具错误率没有统一度量,优化凭感觉。

ch04 要求 Summary 带 `PromptVersion`;当前最小 `Score` 尚未携带该字段。生产评测应把 Prompt、模型、fixture 与 judge 版本一起写入运行清单,否则分数不可复现。

---

## 10.2 概念:金标流 vs 实际流

Eval **不要求**逐字节相等(时间戳、Event ID 会变)。当前比较器先严格筛选目标 `taskID`,再比较事件类型序列与关键 payload。**能力边界**:Round 2 比较器覆盖协议不变量(类型序、CallID 配对、终态、工具计数),**不**覆盖 LLM 措辞质量、成本/延迟统计门禁、随机采样置信区间——这些见 §10.6.1,需独立评测管线。

| 维度 | 含义 |
|------|------|
| Event 数量 | 辅助发现缺失/多余阶段,不能单独证明完整 |
| Event 结构 | 类型顺序与关键 payload(tool name、规范化 arguments、终态等) |
| 终态 TaskStatus | 业务成败 |
| ToolCalls / ToolErrors | 按 CallID 去重后的工具健康度 |
| TokensIn/Out | 同时保留 golden、actual 与 delta;当前不进入 Passed 门禁 |

```go
score := eval.CompareStreams(golden, actual, taskID)
// Passed 要求结构、终态、工具调用数和工具错误数与 golden 一致
```

未知工具会同时产生 `ToolBindFailed` 与错误 `ToolReturned`;两者属于同一个 CallID,只计一次失败。缺失目标 Task 或缺失终态必须失败,Go/Rust 语义一致。

---

## 10.3 ScoreView

对恢复后的 `SessionView` 做轻量检查(任务存在、已终结、终态匹配、Progress 存在且版本达标),适合 Checkpoint 恢复后的冒烟。

---

## 10.4 与观测的关系

ADR-002 五段 span 中的 context message 数、tokens、tool call 数可作为 Eval 的上游信号。Round 2 **不绑定 OTel SDK,也不宣称已映射官方 Semantic Conventions**;生产实现需单独定义版本化属性表,再从 trace 离线聚合回归报告。

---

## 10.5 参考实现

```
runtime-go/eval/{eval.go,ch10_eval_test.go}
runtime-rs/src/eval/mod.rs
runtime-rs/tests/ch10_eval.rs
```

```bash
cd runtime-go && go test ./eval -run TestCh10 -v
cd runtime-rs && cargo test ch10_compare_streams
```

M3 主线的端到端串联(Planner 拆子 Task → Executor 跑工具 → Saga 关父 → Checkpoint 恢复 → Eval 冒烟)在 [`runtime-go/examples/m3/main.go`](../runtime-go/examples/m3/main.go)。示例把本次流复制为 baseline/candidate,只演示 API 接线,不是独立金标证据:

```bash
cd runtime-go && go run ./examples/m3     # 或 go test ./examples/m3
```

---

## 10.6 优化闭环(设计)

```
改 Prompt/Planner → 跑金标套件 → Score 对比基线 → 通过才合并
```

Round 2 交付确定性协议比较器与测试,并在 Pages CI 中运行 Go/Rust 全量测试。真实金标套件仍需版本化 fixture、人工审核的 baseline 更新和敏感数据脱敏;失败运行不得自动覆盖 baseline。

### 10.6.1 非确定性场景

协议不变量(事件合法性、CallID 配对、终态存在)适合单次硬失败;LLM 质量、成本和延迟不适合用一次布尔运行下结论。生产评测至少应:

1. 固定模型版本、Prompt 版本、工具 fixture、temperature 与可用 seed;
2. 无法固定时对每个 case 重复运行,报告成功率及置信区间;
3. 对 token、成本、延迟报告中位数、p95、效应量与 bootstrap 置信区间;
4. 版本比较采用配对 case;二元成功率可用 McNemar 或配对 bootstrap;
5. LLM-as-judge 版本化并校准,盲化候选顺序,记录重判一致性。

门禁应分层:协议/安全错误零容忍;任务成功率不得显著退化;成本与延迟允许在明确预算内波动。

---

## 10.7 取舍记录

| 决策 | 选择 | 代价 | 什么情况下会被推翻 |
|------|------|------|----------|
| 比较粒度 | 不变量而非全文 | 抓不住措辞回归 | 增加 LLM-as-judge 维度 |
| 无 OTel 硬依赖 | 纯 Score 结构 | 与线上 trace 要手写桥 | 引入官方 semantic conventions |
| Passed 定义 | 结构+状态+工具计数与 golden 一致 | 并发合法轨迹可能 false red | 引入偏序比较与套件阈值 |

---

## 10.8 小结

- Eval 把 Agent Runtime 的回归变成结构化 Score。
- 与 Checkpoint 恢复、工具错误率已形成最小衔接;Prompt/模型版本清单仍待扩展。
- M3/M4 当前闭合的是确定性参考路径与协议冒烟,不是生产级随机质量评测平台。

---

## 参考

- Go: [`runtime-go/eval/eval.go`](../runtime-go/eval/eval.go)
- Rust: [`runtime-rs/src/eval/mod.rs`](../runtime-rs/src/eval/mod.rs)
- 流程图:[`diagrams/ch10-eval-pipeline.mmd`](../diagrams/ch10-eval-pipeline.mmd)
- 相关:[ch04 Prompt 版本](ch04-context-engine.md)、[ch06 Prompt Compiler](ch06-prompt-compiler.md)、[ADR-002 观测](../adr/ADR-002-dataflow-protocol.md)
