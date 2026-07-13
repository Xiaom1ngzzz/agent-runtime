# 第 6 章 · Prompt 编译器

> ch04 §4.8 抛出过一个词:**Prompt Compiler**。它是"从 Context 到 Messages"这一段。ch06 把它撕开——为什么"拼字符串"是错的、为什么它更像编译器而不是模板引擎、以及不同 Provider 的差异如何在同一份 Context 上兑现。

---

## 6.1 问题:能跑的字符串,扛不住的四件事

生产上第一版 Agent 里,Prompt 通常是这样写的:

```python
system_prompt = f"""
你是一个 Agent。用户偏好:{user_pref}。
最近历史:
{history}

请合理调用工具。可用工具:
- weather(city, date)
- send_email(to, body)
"""
```

demo 里跑得通。放进任何一个真实系统,下面这四件事迟早一起砸下来:

1. **换 Provider 就崩**。从 OpenAI 换到 Anthropic:OpenAI 用 `tools` 字段传 schema,Anthropic 用 `tools` 但格式不同,Bedrock 又一套。工具描述本来应该走结构化接口,拼进 system prompt 之后每换一次 Provider 都要重写字符串。
2. **上下文一变整个 Prompt 就重生成**。ch04 §4.8 提到 Prompt Caching:Instructions 层应该稳定,但如果 Instructions 里塞了变化的用户偏好,cache 几乎从不命中,成本翻倍。
3. **调试无从下手**。生产上遇到"这次回答很奇怪",想复现——发现没有任何地方能拿到"当时到底发给 LLM 的 Messages 长什么样"。日志里只有一句 `INFO llm called`。
4. **没版本、没测试**。改一句 system prompt,直接推上线。下周产品说效果差了,想回退——不知道改的是哪一句、什么时候改的。

这四件事都不是"prompt engineering 水平"的问题,是**Prompt 缺少工程约束**的问题。

**Prompt 应该是被编译出来的,不是被拼出来的。**

---

## 6.2 概念:为什么是"Compiler"而不是"Template"

看两个直觉:

- **Template 引擎**(如 Jinja、Handlebars):字符串里挖洞,运行时填变量。**输出是字符串**。
- **Compiler**:输入是**结构化的 AST(或近似的 IR)**,输出是**目标语言的合法程序**。中间会做**类型检查**、**优化**、**Provider 特化**。

**Prompt Compiler 靠近后者。**它的输入是 ch04 定义的 `Context`(六层结构化输入):

```
Context {
    SessionID, TaskID, TurnID,
    Messages: []Message,  // 前几层已经排好的 messages
    Tools:    []Tool,     // JSON Schema
}
```

输出是**某个 Provider 认识的合法请求体**:

```
type ProviderRequest struct {
    Model    string
    System   string       // or []Message
    Messages []Message
    Tools    ProviderToolSchema  // OpenAI 格式 or Anthropic 格式
    // ...其它 provider 特有字段
}
```

概念上的 Prompt 编译管线有四个阶段。**Round 2 为保持 `Context { Messages, Tools }` 这个中间表示,把 Layout 落在上游 Project (`LayeredContextEngine.Assemble`)；PromptCompiler 从布局后的 IR 开始执行 Type-check 与 Provider Emit。** Optimize 仍是设计目标:

| 阶段 | 名字 | 做什么 |
|---|---|---|
| **1. Layout** | 布局(Project 落地) | 把 six-layer SessionView 排成中立的 `Context.Messages`(顺序 + role 归属) |
| **2. Type-check** | 类型检查 | 验证 Messages 合法(role 序列合规、tool_call_id 匹配、schema 完整) |
| **3. Optimize** | 优化(设计目标) | 合并连续同 role、去重、prompt cache 边界对齐 |
| **4. Emit** | 生成 | 序列化为具体 Provider 的请求格式(OpenAI/Anthropic/Bedrock/…) |

**为什么这四件事不能省**:

- 没有 **Layout**:六层输入的顺序随手排,LLM 服从度不稳定。
- 没有 **Type-check**:线上遇到 `role=tool` 缺 `tool_call_id`,LLM 直接 400。
- 没有 **Optimize**:每次调用 tokens 都是最大,成本不可控。
- 没有 **Emit**:业务代码里散布 Provider-specific 拼接逻辑,换 Provider 是重构。

从职责上看这仍是一条编译管线;从当前代码边界看,Project 产出布局后的 IR,PromptCompiler 负责校验并把 IR 特化为 Provider 请求。业务代码只关心 Context。

---

## 6.3 六层输入 → Messages 的 Layout 契约(Project 落地)

ch04 §4.8 给了六层到 role 的映射表。这里从**反例**开始展开。

### 6.3.1 反例 1:所有东西塞 system

```
system: "You are agent. User pref: 不吃辣. Doc: 差旅政策... Tool: weather... History: user said 你好, assistant said 你好..."
user:   "帮我订机票"
```

问题:

- system prompt 巨长,不敢开 Prompt Cache(每次都变)
- LLM 分不清"权威指令"和"历史对话"
- 工具描述在自然语言里,LLM 生成 tool call 时经常格式错

### 6.3.2 反例 2:所有东西塞 user

```
system: "You are agent."
user:   "背景:用户不吃辣。文档:差旅政策...。历史:... 现在:帮我订机票"
```

问题:

- LLM 会把"背景"当作用户新说的话去处理
- 一旦"背景"里有指令性内容("你必须…"),LLM 服从度显著低于 system
- 无法区分"哪部分是本次任务",Chain-of-Thought 容易跑偏

### 6.3.3 反例 3:工具描述拼在 system 里,不走 tools 字段

```python
system = """你有以下工具:
- weather(city: str, date: str) — 查天气
- send_email(to: str, body: str) — 发邮件

调用时输出 JSON: {"tool": "...", "args": {...}}
"""
```

问题(在 ch04 §4.8.2 已经论证过):

- 每个 Provider 都有原生 `tools` 字段,不用它 = 主动放弃结构化输出保证
- LLM 输出的 tool call JSON 经常格式错(缺引号、多逗号、名字打错)
- 换 Provider 要改字符串;`tools` 字段是跨 Provider 的抽象

### 6.3.4 正确的 Layout(`LayeredContextEngine` 给出)

```
[system]  Instructions (稳定,cache 边界)
[system]  Task Frame (每 Task 变一次)
[system]  Progress,包在 <task_progress> 标签里
[system]  Compressed History,包在 <prior_summary> 标签里
[user]    UserSpoke #1
[assistant with tool_calls]  LLM #1
[tool]    ToolReturned #1 (tool_call_id 匹配上一条)
[user]    UserSpoke #2
[assistant]  LLM #2
[system]  Memory Refs,包在 <memory_ref> 标签里(位置可选,靠后避免打断对话)
```

Tools 单独走 `Messages.Tools` 字段,**不进 role 序列**。

**为什么 Memory Refs 靠后**:LLM 在长上下文里对位置末尾通常更敏感(依据见文末 *Lost in the Middle*)。参考实现把检索证据放在 Working Set 之后、靠近消息尾部;它不是新一轮 `user` 消息,也不打断 tool_call / tool_result 闭合序列。

---

## 6.4 Type-check:验证 Messages 合法

这一步在多数 Prompt 库里被跳过——直到线上炸了才补。基线校验清单:

| 检查 | 违反后果 | 例子 |
|---|---|---|
| role 属于 `{system, user, assistant, tool}` | Provider 直接拒 | 手滑写成 `"assiatant"` |
| system 消息位置(OpenAI 只允许开头一条;Anthropic 允许分离但推荐开头) | 效果显著差 | system 出现在 user 之后 |
| `role=tool` 必须有 `tool_call_id`,且指向上一条 assistant 的某个 tool_call | Provider 400 | tool response 找不到对应的 tool call |
| `role=assistant with tool_calls` 之后,必须跟对应数量的 tool response 才能再有下一条 assistant | Provider 400 | tool call 未闭合 |
| 连续 user 或连续 assistant | 部分 Provider 报错;有些默默合并 | 合并规则不一致 |
| Tool schema 必须是合法 JSON Schema | Provider 拒 | schema 缺 `type` 字段 |

Compiler 在 Emit 之前跑这些检查。发现问题就返回 `PromptError` 而非硬送——**上层业务通过错误感知,而不是通过 LLM 4xx 反推**。

### 6.4.1 反例:相信 Provider 会兜底

```python
# ❌ 反例:直接发,让 Provider 报错
try:
    resp = client.chat(messages=msgs, tools=tools)
except Exception as e:
    log.error("bad prompt", exc_info=e)  # 日志到底哪里错了?
```

问题:

- Provider 错误信息通常笼统("Invalid request")
- 生产上遇到批量失败时,难以定位是哪条 Messages 结构错了
- 换 Provider 后错误分类完全变

**正确做法**:Compiler 内部校验,`PromptError` 带具体字段("message[3].role=tool has no matching tool_call_id"),日志和监控可以直接对齐字段。

---

## 6.5 Optimize:合并、去重、Cache 边界

生产实现不能忽略优化。以下用一个**说明性估算**展示收益:若 200 轮 Session 朴素 emit 约 60K tokens,合并、去重和缓存对齐后降到 20K,调用侧输入量可减少约 66%。该数字不是本仓库 benchmark;实际收益必须用目标模型 tokenizer 和真实流量测量。

> **实现状态**:Round 2 的 PromptCompiler 尚未实现本节三项 Optimize;当前代码覆盖 Type-check 与 Provider Emit。内容合并与历史裁剪主要由 ch04 Project/Compressor 完成。

### 6.5.1 合并连续同 role

反例:每一条 UserSpoke 都独立成一条 `role=user`,即使中间有多条系统提示夹着。

正确:相邻的同 role message 合并为一条,内容用换行拼。**但要注意 tool_call 的闭合边界**(见 §6.4 表格)。

### 6.5.2 去重

反例:多轮 tool call 后,tool_return 里的相同数据被反复送(比如每次都送 30KB 的差旅政策全文)。

正确:检测到内容哈希相同的 tool_return,**只保留最新一次**,老的替换为一句 `[tool: X returned previously, see message N]`。

### 6.5.3 Prompt Cache 边界对齐

各家 Provider 的 Prompt Caching(Anthropic、OpenAI 都支持)基本原则:**从头开始,连续未变的前缀可以复用**。所以:

- Instructions 必须严格稳定,且放在最前
- Task Frame 变得次频繁(每 Task),放在 Instructions 之后
- 其它变化频繁的层放后面

**具体做法**:Compile 阶段插入显式的 cache 分界标记(不同 Provider 语法不同),标出"到这一条为止是可缓存前缀"。

### 6.5.4 反例:每次 Compile 都从零

```python
# ❌ 反例
def compile(ctx):
    msgs = []
    msgs.append(instructions())  # 每次读磁盘或配置
    msgs.append(task_frame(ctx.task))
    # ...
    return msgs
```

Instructions 从磁盘或配置读:字符串每次生成完全一样,但**对象不同**——Prompt Cache 有些实现基于内容哈希,能命中;基于对象 id 的实现命中不了。

**正确做法**:Compiler 里缓存 Instructions 的编译结果,`(instructions_version) -> compiled_bytes`。

---

## 6.6 Emit:两个 Provider Adapter

参考实现(Round 2 落地)提供**ReferenceCompiler + AnthropicCompiler + OpenAICompiler** 三档:

- **ReferenceCompiler** 是"厂商无关"的普通格式(与 memfakes 里那个 pass-through 一致),用于本地测试和跨 Provider 对比。
- **AnthropicCompiler / OpenAICompiler** 展示"同一份 Context 在两家 Provider 上的差异化 Emit"。**不发真请求**——只生成两家格式的 payload,方便观察差异。

### 6.6.1 差异点(参考实现里能对比)

| 维度 | Anthropic (`messages` API) | OpenAI (`chat.completions`) |
|---|---|---|
| **system 位置** | 独立 `system` 字段,不进 messages 数组 | messages[0].role = "system"(可多条) |
| **Tool schema 字段名** | `input_schema` | `parameters` |
| **Tool 定义位置** | 顶层 `tools` 数组 | 顶层 `tools` 数组,但结构不同 |
| **Tool call 的表达** | assistant message 内 `content` 数组含 `type=tool_use` 项 | assistant message 内 `tool_calls` 数组 |
| **Tool result 的表达** | user message 内 `content` 数组含 `type=tool_result` 项 | 独立 role=`tool` 的 message |
| **连续 user 的处理** | 报错 | 接受(当前实现不主动合并) |
| **Cache 语法** | `cache_control: {type: "ephemeral"}` | 自动前缀 cache,无显式标记 |

**关键**:业务代码写的是 `Context { Messages, Tools }`——一个中立的中间表达。Compiler 内部按 Provider 分派,把中间表达翻译成上面两种格式之一。**换 Provider 只需要换 Compiler 实例**。

### 6.6.2 反例:业务里判 Provider

```go
// ❌ 反例
if provider == "anthropic" {
    msgs = []Message{{Role: "system", Content: sysPrompt}, ...}
} else if provider == "openai" {
    msgs = []Message{... openai-specific ...}
}
resp := client.Chat(msgs)
```

问题:

- Provider 判断散在业务各处,加第三家 = 修 N 个地方
- Message 结构直接绑定某家格式,失去"中立中间表达"的价值
- 单元测试要 mock Provider 才能跑

**正确做法**:业务只造 `Context`,Compile 交给 Compiler。业务代码里**永远看不到 provider-specific 字段**。

---

## 6.7 结构化输出:Tool 调用与 JSON Mode

工程上的高频问题之一:**"如何让 LLM 稳定输出结构化 JSON"**。这一节展开。

### 6.7.1 三种手段,从最弱到最强

| 手段 | 做法 | 强度 | 何时用 |
|---|---|---|---|
| **Prompt 里说要 JSON** | "请以 JSON 格式回答" | 弱,LLM 可能输出 markdown 或额外解释 | 快速原型 |
| **JSON Mode**(OpenAI/Anthropic 均支持) | 强制输出合法 JSON | 中,内容仍可能不匹配 schema | 简单结构 |
| **Function Calling / Tool Use** | 定义 tool schema,LLM 通过 tool 调用返回 | 强,schema 匹配的验证由 Provider 保证 | 生产 |

**基线选择**:任何"需要下游解析"的结构化输出,走 **Tool Use**。这也是为什么 ch04 §4.6 Summary 生成用 tool 而不是 prompt。

### 6.7.2 反例:JSON Mode + Prompt 里指定 schema

```
system: 输出必须匹配这个 schema:{"user_intents": [...], "tool_results": {...}, ...}
```

问题:

- JSON Mode 只保证"合法 JSON",不保证匹配 schema
- LLM 可能输出多余字段或缺字段,需要业务侧再做一遍 schema 校验
- 换 Provider 时"prompt 里带 schema"的写法迁移成本高

**正确做法**:定义一个 `SummaryTool { input_schema: {...} }`,通过 Function Calling 强制 LLM 用 tool。Provider 会拒绝不匹配 schema 的调用——**结构化输出的保证由 Provider 兜底,而不是业务侧 try-catch**。

### 6.7.3 与 ch01 Payload 类型化的一致哲学

ch01 §1.3 论证过:Payload 不用 `map[string]any`,用 marker interface / 封闭 enum。**同样的哲学延伸到 LLM 输出侧**:结构化输出不靠"事后正则",靠"事前约束"。

- 事前:tool schema 是 LLM 输出的合法性契约
- 事中:Provider 内部保证输出匹配 schema
- 事后:业务侧只做业务校验(值域、跨字段约束),不做格式校验

这条一致性让 Runtime 从输入到输出**全链路结构化**,没有"半结构化半自然语言"的模糊地带。

---

## 6.8 Prompt 是版本化资产,不是字符串

回到 §6.1 第 4 件事:"没版本、没测试"。

生产级 Prompt 应该像代码一样管理:

### 6.8.1 至少三条纪律

1. **每个 Prompt 模板有独立版本号**。改一句,版本号 +1。
2. **版本号进 Event 流**。§4.6.1 的 `Summary.PromptVersion` 已经在做这件事——所有由 LLM 生成的产物都带 PromptVersion。
3. **有 eval 集**:每个 Prompt 版本发布前跑一遍标准 case,回归发现后拒绝上线。

### 6.8.2 PromptStore(接口设计)

PromptStore 属于"需要持久化基础设施"的组件,参考实现(内存档)未包含——这里给出接口设计,供接入配置中心 / 数据库时落地:

```go
// PromptStore 接口设计(参考实现未包含,见 §6.11 取舍)
type PromptStore interface {
    Get(name string, version string) (Template, error)
    List() []TemplateMeta
}

type Template struct {
    Name    string
    Version string
    Body    string   // Text 或结构化定义
    Params  map[string]ParamSpec  // 允许的参数(校验时用)
}
```

**关键**:Compile 时**必须显式传版本号**,不允许"用最新的"这种模糊语义。生产上"最新的"总是踩坑——上游改 prompt 忘了通知下游。

### 6.8.3 反例:热更新 Prompt

```
运维:直接编辑生产环境 prompts.yml,重启服务。
```

问题:

- 无法回滚(旧字符串没了)
- 无法关联"这次改动导致的问题"和"具体是哪一版 prompt"
- 无法跑 eval

**正确做法**:Prompt 版本**只增不改**,发布时是"新增一个版本号",生效时是"路由到新版本号"。回滚 = 切回旧版本号,数据依然在。

---

## 6.9 多级降级

对齐 ch02 §2.7 / ch04 §4.9 / ch05 §5.9 的失败模型:

| 触发 | 策略 | Event | 是否终止 Turn |
|---|---|---|---|
| Layout 阶段 Messages 数超预算 | 触发 §4.9 的"drop:oldest" 降级 | `ContextCompressed{strategy="fallback:drop-oldest"}` | 否 |
| Type-check 失败 | 拒绝本次 Compile,拒绝本 Turn 的 LLM 调用 | `TurnEnded{status=failed, reason="compile: ..."}` | 是 |
| Provider 特有字段缺失(如缺 tools 字段但 message 里有 tool_calls) | Type-check 拦截,同上 | 同上 | 是 |
| Prompt Cache 语法错误 | 静默降级为不 cache,记 metric | `PromptCacheDisabled{reason}`(规划中,参考实现未包含) | 否 |
| Tool schema 无效 | 移除该 tool,记录 | `ToolBindFailed{name, reason}`(ch08 随工具注册表落地) | 否(LLM 可能用 fallback 回应) |
| Compile 内部 panic | 上层 Loop 捕获,降级为 memfakes 的 pass-through | `TurnEnded{status=failed, reason="compile-panic"}` | 是 |

**核心哲学**:Compile 是"数据流的最后一段纯函数",出错代价小但可见。**任何降级都在 Event 流里留痕**,与 ADR-002 完全一致。

---

## 6.10 参考实现(Round 2 已落地)

### 6.10.1 目录结构增量

```
runtime-go/
  prompt/
    prompt.go             (已存在: PromptCompiler 接口)
    provider_request.go   (新: Anthropic/OpenAI 请求体类型 + checkMessages 基线校验 + PromptCheckError)
    reference.go          (新: ReferenceCompiler - 中立格式)
    anthropic.go          (新: AnthropicCompiler,含 checkNoConsecutiveUser)
    openai.go             (新: OpenAICompiler)

runtime-rs/src/
  prompt/
    mod.rs                (扩展: ReferenceCompiler/AnthropicCompiler/OpenAICompiler 同文件)
```

PromptStore(§6.8.2)未随本轮落地,见 §6.11 取舍。

职责边界:六层 Layout 与 `<task_progress>` / `<prior_summary>` / `<memory_ref>` 渲染位于 `runtime-go/context/layered.go` 和 `runtime-rs/src/context/layered.rs`;本目录的 Compiler 接收已布局的 `Context.Messages`,执行 Type-check 与 Provider Emit。§6.5 Optimize 为后续扩展。

### 6.10.2 端到端测试:两个 Provider 差异对比

`runtime-go/prompt/ch06_provider_diff_test.go` + `runtime-rs/tests/ch06_provider_diff.rs`:

**场景**:同一个 Context(含 system + user + assistant with tool_calls + tool response + tools list),分别用 Anthropic 和 OpenAI Compiler 编译。断言:

- Anthropic 版本:system 独立字段,不在 messages 数组;tool schema key 为 `input_schema`;tool_use / tool_result 展开为 content 数组项
- OpenAI 版本:system 保留在 messages 里;tool schema key 为 `parameters`;tool response 是独立 `role=tool` 消息
- 连续 user 的 Context:Anthropic Compiler 拒绝,OpenAI Compiler 接受
- Type-check 断言:构造一条 `tool_call_id` 无法匹配任何 assistant tool_call 的孤儿 tool 消息,Compile 返回带具体字段的 `PromptCheckError`

**这份测试也是"业务代码只写 Context,换 Provider 只需换 Compiler"的形式化证据**。

---

## 6.11 取舍记录

| 决策 | 选择 | 代价 | 什么情况下会被推翻 |
|---|---|---|---|
| Compile 命名 | 用编译管线术语;Round 2 的 Layout 在 Project,Compiler 从 IR 开始 | 物理模块与概念阶段不一一对应 | 若未来引入独立 Context IR,可把 Layout 移入 prompt 包 |
| Provider Adapter 深度 | 每 Provider 一个独立 Compiler 实现 | 加 Provider 要写代码,不能 config 化 | 若 Provider 差异极小(只有字段名不同),可以合并成 "TemplateCompiler + config" |
| Type-check 严格度 | Compile 里检查全部 6 条基线规则 | 慢一点(纳秒到微秒) | 极高吞吐场景可以退化为"只在 dev 模式检查",生产靠上游保证 |
| Prompt 版本管理 | 独立 PromptStore + 版本号(接口设计,参考实现未包含) | 需要额外基础设施 | 极小项目可以放代码常量里,但要有版本 tag |
| 结构化输出手段 | 生产强制用 Function Calling | 简单文本对话不该被强套 tool | 纯对话 Agent(如客服)不需要 tool,Prompt Compiler 允许 tools=[] |
| Emit 后的日志 | 每次 Emit 记录 hash + version(而非原文) | 追查具体 Prompt 内容要额外查 PromptStore | 若合规要求存原文,把原文进 Event(占用大) |
| Cache 边界处理 | Compile 里显式插入 cache 标记 | Compile 得知道 Provider 是否支持 cache | 若 Provider 全都自动 cache,标记可以退化为 no-op |

---

## 6.12 小结

- Prompt 不是字符串,是被**编译出来**的合法请求体。
- Prompt 编译管线有 Layout / Type-check / Optimize / Emit 四段;Round 2 的 Layout 在 Project,Optimize 尚未落地。
- 六层 Context → messages 的映射有明确规则,反例包括"全塞 system""全塞 user""tools 拼进 system"。
- Type-check 集中在 Compile 里,业务通过 `PromptError` 感知,而不是通过 Provider 4xx 反推。
- Optimize 的设计目标是合并同 role、去重、Cache 边界对齐;60K → 20K 仅为说明性估算,需用真实流量验证。
- Emit 按 Provider 分派,业务代码只写 Context,换 Provider 换 Compiler 实例即可。
- 结构化输出优先 Function Calling / Tool Use,不靠"prompt 里说要 JSON"。
- Prompt 是版本化资产:每个模板一个版本号,进 Event 流(如 `Summary.PromptVersion`),支持 eval + 回滚。
- 多级降级:Type-check 失败终止 Turn(是),Cache/schema 失效降级继续(否)。

第二部分到此结束。下一章 **第 7 章 · 规划器与任务图** 会展开 Task 层——从扁平任务走向依赖图,以及 Progress 与 Graph 的关系。

---

## 参考

- [ADR-001 · Runtime 边界与职责](../adr/ADR-001-runtime-domain.md)
- [ADR-002 · Runtime 数据流协议](../adr/ADR-002-dataflow-protocol.md)——Compile 是"最后一段纯函数"
- [ADR-003 · Runtime 与 DDD 对应关系](../adr/ADR-003-ddd-mapping.md)——PromptCompiler 是 Domain Service + Anti-Corruption Layer
- 参考实现(Round 2 已落地):
  - Go: [`runtime-go/prompt/provider_request.go`](../runtime-go/prompt/provider_request.go)、[`runtime-go/prompt/reference.go`](../runtime-go/prompt/reference.go)、[`runtime-go/prompt/anthropic.go`](../runtime-go/prompt/anthropic.go)、[`runtime-go/prompt/openai.go`](../runtime-go/prompt/openai.go)
  - Rust: [`runtime-rs/src/prompt/mod.rs`](../runtime-rs/src/prompt/mod.rs)
- 相关章节:`ch04-context-engine.md`(§4.4 Layout 与 §4.8 Compile 引入)、`ch05-memory.md`(§5.5 Memory Refs 由 Project 渲染)、`ch07-planner.md`(Task Graph 与 TaskFrame/Progress 的关系)
- 研究/工程参考:
  - Anthropic, *Prompt Caching* (2024) —— §6.5.3 依据
  - OpenAI, *Structured Outputs* (2024) —— §6.7 Function Calling 依据
  - Nelson Liu et al., *Lost in the Middle: How Language Models Use Long Contexts* (2023) —— §6.3.4 Memory Refs 靠后依据
  - Anthropic, *Tool Use* documentation —— §6.6 Provider 差异依据
