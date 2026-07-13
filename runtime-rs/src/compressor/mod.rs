//! Compressor —— 独立于 Step 的 GC。见 ch04 §4.5。
//!
//! 与 `runtime-go/compressor/compressor.go` 对齐。
//!
//! Compressor 不在 ch02 §2.4 的 5 段协议里 —— 它像 GC,独立、可选、有副作用。
//! 由上层 Loop 触发,产出 ContextCompressed Event 追加到 EventStore。
//! Assemble 只读事实,不参与摘要生成。

use std::sync::{Arc, Mutex};

use crate::domain::{
    Event, EventPayload, PayloadCompressionSkipped, PayloadContextCompressed, Summary,
};
use crate::state::{EventStore, State, StateError};

#[derive(Debug)]
pub struct CompressorError(pub String);

/// §4.5.1 定义的接口。
pub trait Compressor {
    /// 检查当前 Session 需不需要压缩;需要就产出 ContextCompressed Event。
    /// 返回空 Vec 表示"不需要压缩"。
    fn tick(&mut self, session_id: &str) -> Result<Vec<Event>, CompressorError>;
}

/// 抽象"如何生成 Summary"这件事。
/// 生产实现里是 LLM 调用;测试里是 fake/剧本化。
/// Summarizer 是唯一允许 IO 的组件(§4.4.2 反例的正确出口)。
pub trait Summarizer {
    fn summarize(
        &mut self,
        session_id: &str,
        task_id: &str,
        events: &[Event],
    ) -> Result<Summary, CompressorError>;
}

/// "按 WorkingSet 长度触发"的最简 Compressor。§4.5.2。
pub struct ByTurns {
    pub state: Arc<Mutex<dyn State + Send>>,
    pub store: Arc<Mutex<dyn EventStore + Send>>,
    pub summarizer: Box<dyn Summarizer + Send>,
    pub threshold: usize,
}

impl Compressor for ByTurns {
    fn tick(&mut self, session_id: &str) -> Result<Vec<Event>, CompressorError> {
        let threshold = if self.threshold == 0 {
            3
        } else {
            self.threshold
        };

        let view = {
            let st = self.state.lock().unwrap();
            st.view(session_id)
                .map_err(|e: StateError| CompressorError(e.0))?
        };

        // 找出未 Superseded 且属于同一 Task 的 TurnDigest 序列。
        let mut target = Vec::new();
        let mut task_id = String::new();
        for d in &view.working_set {
            if d.superseded {
                continue;
            }
            if task_id.is_empty() {
                task_id = d.task_id.clone();
            }
            if d.task_id != task_id {
                break;
            }
            target.push(d.clone());
        }
        if target.len() < threshold {
            return Ok(Vec::new());
        }

        // 只压缩最老的一半(保护最近对话)。
        let to_compress = &target[..target.len() / 2];
        if to_compress.is_empty() {
            return Ok(Vec::new());
        }
        let from_seq = to_compress[0].from_seq;
        let to_seq = to_compress[to_compress.len() - 1].to_seq;

        // 拉原文。
        let events: Vec<Event> = {
            let s = self.store.lock().unwrap();
            s.load_from(session_id, from_seq - 1)
                .map_err(|e| CompressorError(e.0))?
        };
        let scoped: Vec<Event> = events
            .into_iter()
            .filter(|e| e.seq >= from_seq && e.seq <= to_seq)
            .collect();

        // 调 Summarizer。
        match self.summarizer.summarize(session_id, &task_id, &scoped) {
            Ok(mut summary) => {
                summary.session_id = session_id.into();
                summary.task_id = task_id.clone();
                summary.from_seq = from_seq;
                summary.to_seq = to_seq;
                Ok(vec![Event {
                    id: String::new(),
                    session_id: session_id.into(),
                    task_id: task_id.clone(),
                    turn_id: String::new(),
                    ts: None,
                    caused_by: String::new(),
                    seq: 0,
                    payload: EventPayload::ContextCompressed(PayloadContextCompressed {
                        from_seq,
                        to_seq,
                        strategy: format!("turns:{}", threshold),
                        summary,
                        from_tokens: 0,
                        to_tokens: 0,
                    }),
                }])
            }
            Err(err) => {
                // 降级:追加 CompressionSkipped Event。
                Ok(vec![Event {
                    id: String::new(),
                    session_id: session_id.into(),
                    task_id: task_id.clone(),
                    turn_id: String::new(),
                    ts: None,
                    caused_by: String::new(),
                    seq: 0,
                    payload: EventPayload::CompressionSkipped(PayloadCompressionSkipped {
                        reason: "summarizer_error".into(),
                        detail: err.0,
                    }),
                }])
            }
        }
    }
}

// ---------- 剧本化 Summarizer for tests ----------

pub struct ScriptedSummarizer {
    script: Vec<Summary>,
    idx: usize,
}

impl ScriptedSummarizer {
    pub fn new(script: Vec<Summary>) -> Self {
        Self { script, idx: 0 }
    }
}

impl Summarizer for ScriptedSummarizer {
    fn summarize(
        &mut self,
        _session_id: &str,
        _task_id: &str,
        _events: &[Event],
    ) -> Result<Summary, CompressorError> {
        if self.idx >= self.script.len() {
            return Err(CompressorError("scripted summarizer exhausted".into()));
        }
        let out = self.script[self.idx].clone();
        self.idx += 1;
        Ok(out)
    }
}
