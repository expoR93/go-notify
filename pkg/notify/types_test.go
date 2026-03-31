package notify

import (
	"testing"
	// "github.com/sony/sonyflake"
)

type TestPayload struct {
	Message string
}

func TestNewNotificationEvent(t *testing.T) {
	ev := newNotificationEvent(123, ChannelEmail, TestPayload{Message: "test"})

	if ev.Attempt != 1 {
		t.Errorf("Expected field Attempt to be 1, got %v instead", ev.Attempt)
	}

	if ev.CreatedAt.IsZero() {
		t.Errorf("Expected sane timestamp, got %v instead", ev.CreatedAt)
	}

	if ev.Metadata == nil {
		t.Error("Expected Metadata shouldn't be nil")
	}
}

func TestManagerCreateEvent(t *testing.T) {
	events := []NotificationEvent[TestPayload]{}

	m := NewManager[TestPayload](Config{MachineID: 40})

	for i := 0; i < 2; i++ {
		ev, err := m.CreateEvent(ChannelEmail, TestPayload{Message: "test"})
		if err != nil {
			t.Fatalf("Failed to create event: %v", err)
		}
		events = append(events, ev)
	}

	if events[0].EventID == 0 || events[1].EventID == 0 {
		t.Error("Expected event ID != 0")
	}

	if events[0].EventID == events[1].EventID {
		t.Error("Expected different event IDs")
	}
}
