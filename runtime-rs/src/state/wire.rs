//! Event 的 JSON 序列化 —— 与 `runtime-go/state/wire.go` 对齐。
//!
//! 参见 ch03 §3.3.2 与 §3.7.1:
//!   - `EventWire` 是"落盘/传输"用的 DTO,与 `domain::Event` 一一对齐;
//!   - `EventPayload` 已通过 `#[serde(tag="type", content="payload")]` 内置了分派,
//!     Rust 侧无需 factory 表——新增 variant 忘了在 `event_payloads.rs` 里加,编译不过;
//!   - 时间戳走 RFC3339 字符串,兼容跨语言比对(Go 用 `time.Time`)。

use serde::{Deserialize, Serialize};

use crate::domain::{Event, EventPayload};

/// 与 Go 版 `EventDTO` 对齐的 wire-format 表示。
///
/// `payload` 展平后带 `type` + `payload` 两个字段(见 `EventPayload` 的 serde attr),
/// 使得 wire 层的 JSON 与 Go 版完全一致。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EventWire {
    pub id: String,
    pub session_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub task_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub turn_id: String,
    #[serde(default)]
    pub ts_millis: i64, // RFC3339 折衷:先用 epoch millis,跨语言比对最直接。
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub caused_by: String,
    #[serde(default)]
    pub seq: i64,
    #[serde(flatten)]
    pub payload: EventPayload,
}

impl From<&Event> for EventWire {
    fn from(e: &Event) -> Self {
        let ts_millis = e
            .ts
            .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
            .map(|d| d.as_millis() as i64)
            .unwrap_or(0);
        EventWire {
            id: e.id.clone(),
            session_id: e.session_id.clone(),
            task_id: e.task_id.clone(),
            turn_id: e.turn_id.clone(),
            ts_millis,
            caused_by: e.caused_by.clone(),
            seq: e.seq,
            payload: e.payload.clone(),
        }
    }
}

impl From<EventWire> for Event {
    fn from(w: EventWire) -> Self {
        let ts = if w.ts_millis > 0 {
            std::time::UNIX_EPOCH
                .checked_add(std::time::Duration::from_millis(w.ts_millis as u64))
        } else {
            None
        };
        Event {
            id: w.id,
            session_id: w.session_id,
            task_id: w.task_id,
            turn_id: w.turn_id,
            ts,
            caused_by: w.caused_by,
            payload: w.payload,
            seq: w.seq,
        }
    }
}

/// 序列化一条 Event 为 JSON 字节。
pub fn marshal_event(e: &Event) -> Result<Vec<u8>, serde_json::Error> {
    serde_json::to_vec(&EventWire::from(e))
}

/// 反序列化一条 Event。未知 EventType 走 serde 报错;是否退化为"跳过"由 State 层决定(§3.5.3)。
pub fn unmarshal_event(data: &[u8]) -> Result<Event, serde_json::Error> {
    let wire: EventWire = serde_json::from_slice(data)?;
    Ok(wire.into())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::domain::{
        Budget, PayloadContextCompressed, PayloadTaskCreated, PayloadToolCalled,
        PayloadToolReturned, PayloadTurnEnded, PayloadUserSpoke, Summary, TurnStatus,
    };

    fn round_trip(payload: EventPayload) {
        let original = Event {
            id: "e42".into(),
            session_id: "s1".into(),
            task_id: "t1".into(),
            turn_id: "r1".into(),
            ts: None,
            caused_by: "e41".into(),
            payload,
            seq: 42,
        };
        let data = marshal_event(&original).expect("marshal");
        let got = unmarshal_event(&data).expect("unmarshal");
        assert_eq!(got.id, original.id);
        assert_eq!(got.session_id, original.session_id);
        assert_eq!(got.seq, original.seq);
        assert_eq!(got.caused_by, original.caused_by);
        // Payload equality via debug 是最简单的可比较方式(EventPayload 未派生 PartialEq)。
        assert_eq!(format!("{:?}", got.payload), format!("{:?}", original.payload));
    }

    #[test]
    fn round_trip_user_spoke() {
        round_trip(EventPayload::UserSpoke(PayloadUserSpoke {
            text: "hello 世界".into(),
        }));
    }

    #[test]
    fn round_trip_tool_called() {
        round_trip(EventPayload::ToolCalled(PayloadToolCalled {
            call_id: "c1".into(),
            name: "weather".into(),
            arguments: r#"{"city":"北京"}"#.into(),
        }));
    }

    #[test]
    fn round_trip_tool_returned() {
        round_trip(EventPayload::ToolReturned(PayloadToolReturned {
            call_id: "c1".into(),
            content: r#"{"temp":26}"#.into(),
            is_error: false,
        }));
    }

    #[test]
    fn round_trip_turn_ended() {
        round_trip(EventPayload::TurnEnded(PayloadTurnEnded {
            status: TurnStatus::Done,
            tokens_in: 520,
            tokens_out: 48,
            cost_us: 0.0,
            latency_ms: 2100,
        }));
    }

    #[test]
    fn round_trip_task_created() {
        round_trip(EventPayload::TaskCreated(PayloadTaskCreated {
            goal: "查天气".into(),
            budget: Budget {
                max_tokens: 8000,
                ..Default::default()
            },
        }));
    }

    #[test]
    fn round_trip_context_compressed() {
        round_trip(EventPayload::ContextCompressed(PayloadContextCompressed {
            from_seq: 100,
            to_seq: 200,
            strategy: "turns:8".into(),
            from_tokens: 8000,
            to_tokens: 2000,
            summary: Summary {
                session_id: "s1".into(),
                task_id: "t1".into(),
                from_seq: 100,
                to_seq: 200,
                user_intents: vec!["查天气".into()],
                model_used: "claude-opus-4-7".into(),
                confidence: 0.9,
                ..Default::default()
            },
        }));
    }

    #[test]
    fn unknown_type_is_reported() {
        let raw = br#"{"id":"e1","session_id":"s1","type":"FutureEvent","payload":{}}"#;
        let err = unmarshal_event(raw).unwrap_err();
        // serde 会报未知 variant——wire 层如实透传;State 层再决定策略(§3.5.3)。
        assert!(err.to_string().contains("FutureEvent") || err.to_string().contains("unknown"));
    }
}
