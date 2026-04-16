package notify

import (
	"testing"
	"time"
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

	if ev.Metadata != nil {
		t.Errorf("Expected Metadata to be nil for zero-allocation initialization, got %v", ev.Metadata)
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

func TestValidateEvent(t *testing.T) {
	testCases := []struct {
		name          string
		inputEvent    NotificationEvent[TestPayload]
		expectedError string
	}{
		{
			name: "EventID validation",
			inputEvent: NotificationEvent[TestPayload]{
				EventID: 0,
			},
			expectedError: "event_id cannot be zero",
		},
		{
			name: "Attempt validation",
			inputEvent: NotificationEvent[TestPayload]{
				EventID: 1,
				Attempt: 0,
			},
			expectedError: "event_attempt cannot be zero",
		},
		{
			name: "Channel validation",
			inputEvent: NotificationEvent[TestPayload]{
				EventID: 2,
				Attempt: 1,
				Channel: "",
			},
			expectedError: "event_channel cannot be empty",
		},
		{
			name: "CreatedAt.After() validation",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   3,
				Attempt:   2,
				Channel:   ChannelEmail,
				CreatedAt: time.Now().Add(10 * time.Second),
			},
			expectedError: "event_createdat cannot be further in the future (5s+), potential serialization error or a severely de-synchronized clock",
		},
		{
			name: "CreatedAt.IsZero() validation",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   5,
				Attempt:   4,
				Channel:   ChannelEmail,
				CreatedAt: time.Time{},
			},
			expectedError: "event_createdat cannot be zero",
		},
		{
			name: "Successful validation",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   100,
				Attempt:   1,
				Channel:   ChannelEmail,
				CreatedAt: time.Now(),
			},
			expectedError: "", // No error expected
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.inputEvent.Validate()

			if tc.expectedError != "" && err == nil {
				t.Errorf("expected error %q, but got nil", tc.expectedError)
				return
			}

			if tc.expectedError == "" && err != nil {
				t.Errorf("expected success, but got error: %v", err)
				return
			}

			if err != nil && err.Error() != tc.expectedError {
				t.Errorf("expected error %q, but got %q", tc.expectedError, err.Error())
			}
		})
	}
}

func TestIsExpired(t *testing.T) {
	testCases := []struct {
		name     string
		event    NotificationEvent[TestPayload]
		expected bool
	}{
		{
			name: "Not expired (ExpiresAt is zero)",
			event: NotificationEvent[TestPayload]{
				ExpiresAt: time.Time{},
			},
			expected: false,
		},
		{
			name: "Not expired (ExpiresAt in the future)",
			event: NotificationEvent[TestPayload]{
				ExpiresAt: time.Now().Add(1 * time.Hour),
			},
			expected: false,
		},
		{
			name: "Expired (ExpiresAt in the past)",
			event: NotificationEvent[TestPayload]{
				ExpiresAt: time.Now().Add(-1 * time.Hour),
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.event.IsExpired() != tc.expected {
				t.Errorf("expected IsExpired() to be %v, got %v", tc.expected, !tc.expected)
			}
		})
	}
}
