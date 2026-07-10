package state

import (
	"testing"

	"agent-runtime-go/domain"
)

// TestWireRoundTrip 证明 §3.3.2 的核心不变量:
// Event -> JSON -> Event 得到相等的 Event(payload 逐字段一致)。
func TestWireRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		payload domain.EventPayload
		typ     domain.EventType
	}{
		{"UserSpoke", domain.PayloadUserSpoke{Text: "hello 世界"}, domain.EvtUserSpoke},
		{"ToolCalled", domain.PayloadToolCalled{
			CallID: "c1", Name: "weather", Arguments: `{"city":"北京"}`,
		}, domain.EvtToolCalled},
		{"ToolReturned", domain.PayloadToolReturned{
			CallID: "c1", Content: `{"temp":26}`, IsError: false,
		}, domain.EvtToolReturned},
		{"TurnEnded", domain.PayloadTurnEnded{
			Status: domain.TurnDone, TokensIn: 520, TokensOut: 48, LatencyMS: 2100,
		}, domain.EvtTurnEnded},
		{"TaskCreated", domain.PayloadTaskCreated{
			Goal: "查天气", Budget: domain.Budget{MaxTokens: 8000},
		}, domain.EvtTaskCreated},
		{"ContextCompressed", domain.PayloadContextCompressed{
			FromTokens: 8000, ToTokens: 2000, Strategy: "summary-v1",
		}, domain.EvtContextCompressed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := domain.Event{
				ID: "e42", SessionID: "s1", TaskID: "t1", TurnID: "r1",
				Type: tc.typ, Payload: tc.payload, CausedBy: "e41", Seq: 42,
			}
			data, err := MarshalEvent(original)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := UnmarshalEvent(data)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.ID != original.ID || got.SessionID != original.SessionID {
				t.Fatalf("id/session mismatch: got %+v", got)
			}
			if got.Type != original.Type {
				t.Fatalf("type: got %s, want %s", got.Type, original.Type)
			}
			if got.Seq != original.Seq || got.CausedBy != original.CausedBy {
				t.Fatalf("seq/caused_by: got %+v", got)
			}
			if got.Payload != original.Payload {
				t.Fatalf("payload mismatch: got %#v, want %#v", got.Payload, original.Payload)
			}
		})
	}
}

// TestWireUnknownType 证明 §3.8 wire 层对 unknown type 的处理:如实报错,
// 由 State 层决定是"跳过"还是"拒绝"。
func TestWireUnknownType(t *testing.T) {
	data := []byte(`{"id":"e1","session_id":"s1","type":"FutureEvent","payload":{}}`)
	_, err := UnmarshalEvent(data)
	if err == nil {
		t.Fatal("expected error for unknown event type")
	}
}
