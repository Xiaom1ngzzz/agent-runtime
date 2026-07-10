//! Agent Runtime — Rust 参考实现骨架。
//! Go 版见 `runtime-go/`；两者共享同一套章节与设计(见书 chapters/)。
//!
//! 章节示例(如第一章的 Event 流样本)放在 `examples/ch<NN>/` 下,不进 crate。

pub mod domain;
pub mod event_payloads;

pub mod context;
pub mod prompt;
pub mod llm;
pub mod executor;
pub mod state;

pub mod runtime;
pub mod snapshot;
pub mod wire;
