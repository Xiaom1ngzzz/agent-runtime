# ADR-008: Runtime 安全边界

- **状态**: Accepted
- **日期**: 2026-07-13
- **决策者**: —
- **上游**: [ADR-001 · Runtime 边界](ADR-001-runtime-domain.md)
- **落地章节**: [ch05 · 记忆架构](../chapters/ch05-memory.md), [ch08 · 执行器](../chapters/ch08-executor.md), [ch11 · 生产边界](../chapters/ch11-production-boundaries.md)

## Context

Runtime 记录事实(ch01 审计表述已收窄为因果追溯,非授权裁决)。生产仍需防止:跨租户 Memory 泄漏、未校验 tool 参数注入、超大 tool 输出 DoS、未授权 Principal 写 Session。

## Decision

1. **身份**:每个 Session 绑定 `Principal`;写路径校验调用方 Principal 与 Session 一致。
2. **租户**:Memory `TenantID` 强制过滤(ch05 §5.3);向量检索不得省略 tenant 谓词。
3. **工具**:注册 schema 校验 + 输出截断(ch08 §8.3.1);高危工具须上层策略白名单,Runtime 不替代 IAM。
4. **数据面**:Event/Memory 导出须按 Principal/tenant 脱敏;Archive 访问独立审计。
5. **边界声明**:Runtime **不提供**审批工作流、RBAC 引擎——相邻系统通过 Policy Service 在 Loop 层拦截,Runtime 只记录 `ToolCalled` 事实。

## Consequences

### 正向

- 安全责任清晰:Runtime = 追溯 + 协议 enforcement 的薄层。

### 负向

- 完整零信任须上层 Loop 与 IAM 配合;
- tenant 字段增加所有 Memory 查询样板。

## Alternatives

**A. Runtime 内建 RBAC** —— 违背 ADR-001 边界最小。

**B. 完全信任内网** —— 不满足多租户 SaaS。

## References

- ch01 §1.4.1 审计行, ch05 TenantID, ch08 工具校验
