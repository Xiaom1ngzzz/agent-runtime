// Package state / wire.go —— Event 的 JSON 序列化。
//
// 参见 ch03 §3.3.2 与 §3.7.1:
//   - EventDTO 是"落盘/传输"用的 DTO,与 domain.Event 一一对齐;
//   - payloadFactory 表把 EventType 字符串映射到"空 payload 实例",
//     反序列化时靠它把 json.RawMessage 塞进具体 struct;
//   - 新增 EventType 必须在 payloadFactory 里加一行,否则会拿到 unknown type 错误。
//     严格模式下这就是 §3.8 "Fold 拒绝服务"的第一层拦截。
package state

import (
	"encoding/json"
	"fmt"
	"time"

	"agent-runtime-go/domain"
)

// EventDTO 是 domain.Event 的 wire-format 表示。字段与 domain.Event 一一对齐;
// Payload 用 json.RawMessage 延后解码,交由 payloadFactory 分派。
type EventDTO struct {
	ID        string           `json:"id"`
	SessionID string           `json:"session_id"`
	TaskID    string           `json:"task_id,omitempty"`
	TurnID    string           `json:"turn_id,omitempty"`
	Type      domain.EventType `json:"type"`
	Payload   json.RawMessage  `json:"payload"`
	TS        time.Time        `json:"ts"`
	CausedBy  string           `json:"caused_by,omitempty"`
	Seq       int64            `json:"seq"`
}

// payloadFactory 决定"type 字符串 → 空 payload 实例"。
// 新增 EventType 必须在此加一行,否则反序列化拿到 unknown type 错误(§3.8)。
var payloadFactory = map[domain.EventType]func() domain.EventPayload{
	domain.EvtSessionOpened:      func() domain.EventPayload { return &domain.PayloadSessionOpened{} },
	domain.EvtTaskCreated:        func() domain.EventPayload { return &domain.PayloadTaskCreated{} },
	domain.EvtTaskEnded:          func() domain.EventPayload { return &domain.PayloadTaskEnded{} },
	domain.EvtTurnStarted:        func() domain.EventPayload { return &domain.PayloadTurnStarted{} },
	domain.EvtTurnEnded:          func() domain.EventPayload { return &domain.PayloadTurnEnded{} },
	domain.EvtUserSpoke:          func() domain.EventPayload { return &domain.PayloadUserSpoke{} },
	domain.EvtLLMRequested:       func() domain.EventPayload { return &domain.PayloadLLMRequested{} },
	domain.EvtLLMReplied:         func() domain.EventPayload { return &domain.PayloadLLMReplied{} },
	domain.EvtToolCalled:         func() domain.EventPayload { return &domain.PayloadToolCalled{} },
	domain.EvtToolReturned:       func() domain.EventPayload { return &domain.PayloadToolReturned{} },
	domain.EvtContextCompressed:  func() domain.EventPayload { return &domain.PayloadContextCompressed{} },
	domain.EvtCompressionSkipped: func() domain.EventPayload { return &domain.PayloadCompressionSkipped{} },
	domain.EvtProgressUpdated:    func() domain.EventPayload { return &domain.PayloadProgressUpdated{} },
	domain.EvtMemoryQueried:      func() domain.EventPayload { return &domain.PayloadMemoryQueried{} },
	domain.EvtSubTaskSpawned:     func() domain.EventPayload { return &domain.PayloadSubTaskSpawned{} },
	domain.EvtToolBindFailed:     func() domain.EventPayload { return &domain.PayloadToolBindFailed{} },
}

// MarshalEvent 序列化一条 Event 为 JSON。Type 已由 domain.Event 显式携带,
// 直接嵌入 wire 层,无需运行时反射。
func MarshalEvent(e domain.Event) ([]byte, error) {
	pl, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	dto := EventDTO{
		ID:        e.ID,
		SessionID: e.SessionID,
		TaskID:    e.TaskID,
		TurnID:    e.TurnID,
		Type:      e.Type,
		Payload:   pl,
		TS:        e.TS,
		CausedBy:  e.CausedBy,
		Seq:       e.Seq,
	}
	return json.Marshal(dto)
}

// UnmarshalEvent 反序列化一条 Event。未知 EventType 返回错误——
// 是否退化为"跳过"是 State 层的策略决定(§3.5.3),wire 层只负责如实报告。
func UnmarshalEvent(data []byte) (domain.Event, error) {
	var dto EventDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return domain.Event{}, fmt.Errorf("unmarshal event: %w", err)
	}
	factory, ok := payloadFactory[dto.Type]
	if !ok {
		return domain.Event{}, fmt.Errorf("unknown event type: %s", dto.Type)
	}
	payloadPtr := factory()
	if err := json.Unmarshal(dto.Payload, payloadPtr); err != nil {
		return domain.Event{}, fmt.Errorf("unmarshal payload (%s): %w", dto.Type, err)
	}
	return domain.Event{
		ID:        dto.ID,
		SessionID: dto.SessionID,
		TaskID:    dto.TaskID,
		TurnID:    dto.TurnID,
		Type:      dto.Type,
		Payload:   derefPayload(payloadPtr),
		TS:        dto.TS,
		CausedBy:  dto.CausedBy,
		Seq:       dto.Seq,
	}, nil
}

// derefPayload 把 factory 返回的 *PayloadX 展平成 PayloadX(EventPayload 存值,不存指针)。
func derefPayload(p domain.EventPayload) domain.EventPayload {
	switch v := p.(type) {
	case *domain.PayloadSessionOpened:
		return *v
	case *domain.PayloadTaskCreated:
		return *v
	case *domain.PayloadTaskEnded:
		return *v
	case *domain.PayloadTurnStarted:
		return *v
	case *domain.PayloadTurnEnded:
		return *v
	case *domain.PayloadUserSpoke:
		return *v
	case *domain.PayloadLLMRequested:
		return *v
	case *domain.PayloadLLMReplied:
		return *v
	case *domain.PayloadToolCalled:
		return *v
	case *domain.PayloadToolReturned:
		return *v
	case *domain.PayloadContextCompressed:
		return *v
	case *domain.PayloadCompressionSkipped:
		return *v
	case *domain.PayloadProgressUpdated:
		return *v
	case *domain.PayloadMemoryQueried:
		return *v
	case *domain.PayloadSubTaskSpawned:
		return *v
	case *domain.PayloadToolBindFailed:
		return *v
	default:
		return p
	}
}
