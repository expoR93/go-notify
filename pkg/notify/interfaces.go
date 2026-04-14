package notify

import (
	"context"
	"time"
)

type Driver[T any] interface {
	Listen(ctx context.Context) (<-chan NotificationEvent[T], error)
	Ack(eventID uint64) error
	Nack(event NotificationEvent[T], delay time.Duration) error
}

type Provider[T any] interface {
	Send(ctx context.Context, event NotificationEvent[T]) error
	Type() string // "email", "sms"...
}

type DeadLetterHook[T any] interface {
	Handle(event NotificationEvent[T], finalErr error) error
}
