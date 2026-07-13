# Chapter 10 · Evaluation & Optimization

> 前面九章把 Runtime 建成可跑、可恢复的系统。这一章回答:**怎么知道改 Prompt / Planner / 工具绑定没有把系统弄坏?**最小答案是一个结构化的 **Eval** 框架——对比金标事件流与实际事件流,产出可断言的 Score。

---

## 10.1 问题:没有回归,就没有演进

1. 改一句 Instructions,线上行为漂了,没有本地红灯。
2. "看起来差不多"无法进 CI。
3. Token / 工具错误率没有统一度量,优化凭感觉。

ch04 要求 Prompt 带版本、Summary 带 `PromptVersion`;评测是把这些版本**钉在分数上**的钩子。

---

## 10.2 概念:金标流 vs 实际流

Eval **不要求**逐字节相等(时间戳、Event ID 会变)。比较不变量:

| 维度 | 含义 |
|------|------|
| Event 数量 | 同场景下阶段是否完整 |
| 终态 TaskStatus | 业务成败 |
| ToolCalls / ToolErrors | 工具健康度 |
| TokensIn/Out(可选) | 成本回归 |

```go
score := eval.CompareStreams(golden, actual, taskID)
// score.Passed == EventCountMatch && StatusMatch && ToolErrorRate==0
```

---

## 10.3 ScoreView

对恢复后的 `SessionView` 做轻量检查(终态 + Progress 版本),适合 Checkpoint 恢复后的冒烟。

---

## 10.4 与观测的关系

ADR-002 五段 span 属性(context_msg_count、tokens、tool_call_count)是 Eval 的上游信号。Round 2 **不绑定 OTel SDK**;Score 结构预留与 span 对齐的字段。生产可把同名字段打进 trace,再离线聚合成回归报告。

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

---

## 10.6 优化闭环(设计)

```
改 Prompt/Planner → 跑金标套件 → Score 对比基线 → 通过才合并
```

Round 2 交付比较器与测试;金标套件扩容、自动 baseline 更新属于工程化扩展。

---

## 10.7 取舍记录

| 决策 | 选择 | 代价 | 推翻条件 |
|------|------|------|----------|
| 比较粒度 | 不变量而非全文 | 抓不住措辞回归 | 增加 LLM-as-judge 维度 |
| 无 OTel 硬依赖 | 纯 Score 结构 | 与线上 trace 要手写桥 | 引入官方 semantic conventions |
| Passed 定义 | 数量+状态+零工具错 | 可能过严/过松 | 按套件配置阈值 |

---

## 10.8 小结

- Eval 把 Agent Runtime 的回归变成结构化 Score。
- 与 Prompt 版本、Checkpoint 恢复、工具错误率自然衔接。
- 至此 M3/M4 主线闭合:能规划、能执行、能恢复、能评测。

---

## 参考

- Go: [`runtime-go/eval/`](../runtime-go/eval/)
- Rust: [`runtime-rs/src/eval/`](../runtime-rs/src/eval/)
- 相关:`ch04` Prompt 版本、`ch06`、ADR-002 观测
