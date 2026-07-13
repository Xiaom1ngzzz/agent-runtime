//! Agent Runtime — Rust 参考实现骨架。
//! Go 版见 `runtime-go/`；两者共享同一套章节与设计(见书 chapters/)。
//!
//! 章节示例(如第一章的 Event 流样本)放在 `examples/ch<NN>/` 下,不进 crate。
//! 模块目录与 `runtime-go/<pkg>/` 一一对应。

pub mod domain;
pub mod context;
pub mod state;
pub mod compressor;
pub mod memory;
pub mod prompt;
pub mod runtime;
pub mod executor;
pub mod llm;
