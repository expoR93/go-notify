package notify

import (
	"time"

	"github.com/sony/sonyflake"
)

type Config struct {
	MachineID uint16
}

type NotificationEvent[T any] struct {
	// Identity & Routing (Infrastructure)
	EventID   uint64            `json:"event_id"`
	Channel   string            `json:"channel"`
	CreatedAt time.Time         `json:"created_at"`
	Attempt   int               `json:"attempt"`
	Metadata  map[string]string `json:"metadata"`
	Payload   T                 `json:"payload"`
}

func newNotificationEvent[T any](id uint64, channel string, payload T) NotificationEvent[T] {

	return NotificationEvent[T]{
		EventID:   id,
		Channel:   channel,
		CreatedAt: time.Now(),
		Attempt:   1,
		Metadata:  make(map[string]string),
		Payload:   payload,
	}
}

type Manager[T any] struct {
	flake *sonyflake.Sonyflake
}

func NewManager[T any](cfg Config) *Manager[T] {
	st := sonyflake.Settings{
		MachineID: func() (uint16, error) {
			return cfg.MachineID, nil
		},
	}

	return &Manager[T]{
		flake: sonyflake.NewSonyflake(st),
	}
}

func (m *Manager[T]) CreateEvent(channel string, payload T) (NotificationEvent[T], error) {
	id, err := m.flake.NextID()
	if err != nil {
		return NotificationEvent[T]{}, err
	}

	return newNotificationEvent(id, channel, payload), nil
}
