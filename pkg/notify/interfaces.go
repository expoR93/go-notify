package notify

import (
	"context"
)

type Driver[T any] interface {
	Listen(ctx context.Context) (<-chan NotificationEvent[T], error)
	Ack(eventID uint64) error
	Nack(eventID uint64) error
}

type Provider[T any] interface {
	Send(event NotificationEvent[T]) error
	Type() string // "email", "sms"...
}
