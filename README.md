# Agent Runtime

从第一原理构建 Agent Runtime —— 一本关于 Agent 运行时设计与实现的书,配套 Go / Rust 两份参考实现。

在线阅读: <https://xiaom1ngzzz.github.io/agent-runtime/>

## 仓库结构

- `BOOK.md` — 目标与写作原则。
- `STYLE_GUIDE.md` — 写作约定。
- `ROADMAP.md` — 全书大纲与进度。
- `chapters/` — 章节正文。
- `adr/` — 架构决策记录。
- `runtime-go/` — Go 参考实现(`domain/`、`context/`、`state/` …)。`cd runtime-go && go build ./...`。
- `runtime-rs/` — Rust 参考实现,与 Go 逐字段对齐。`cd runtime-rs && cargo build`。
- `diagrams/` — 架构与时序图源文件。
- `docs/` — MkDocs 站点入口(符号链接,不修改)。

## 阅读顺序

从 `BOOK.md` 开始,然后 `ROADMAP.md`,再按章节顺序读。

## 本地预览站点

```bash
pip install --user mkdocs-material
mkdocs serve
```

浏览器访问 <http://127.0.0.1:8000>。
