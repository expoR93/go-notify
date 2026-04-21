# go-notify

A high-performance, production-ready notification engine for Go with pluggable middleware, automatic retries, circuit breaker pattern, and support for multiple notification channels (Email, SMS, Push, etc.).

## Features

- 🚀 **Blazing Fast** - 44 ns/op baseline performance with zero allocations
- 🔌 **Pluggable Middleware** - Extensible architecture for custom processing logic
- 🔄 **Automatic Retries** - Exponential backoff with configurable max retries
- 🛡️ **Circuit Breaker** - Fault tolerance with automatic recovery
- 📨 **Multi-Channel** - Email, SMS, Push notifications, and more
- 💾 **Dead Letter Queue** - Handle failed notifications gracefully
- 🎯 **Type-Safe Generics** - Fully generic with Go 1.18+ type parameters
- ✅ **Well-Tested** - 35 comprehensive tests with excellent coverage
- 📊 **Observable** - Structured logging and metrics integration points

## Installation

```bash
go get github.com/expoR93/go-notify
```

## Quick Start

```go
package main

import (
	"context"
	"log/slog"
	
	"github.com/expoR93/go-notify/pkg/notify"
)

type EmailPayload struct {
	To      string
	Subject string
	Body    string
}

func main() {
	// 1. Create a driver (message queue interface)
	driver := createYourDriver() // e.g., Redis, RabbitMQ, etc.
	
	// 2. Create providers for each channel
	emailProvider := createEmailProvider()
	
	// 3. Initialize the engine
	engine, err := notify.NewEngine(
		driver,
		[]notify.Provider[EmailPayload]{emailProvider},
		4, // workers
		slog.Default(),
		nil, // dead letter hook
	)
	if err != nil {
		panic(err)
	}
	
	// 4. Start processing
	ctx := context.Background()
	go engine.Start(ctx)
	
	// 5. Send notifications
	manager := notify.NewManager[EmailPayload](notify.Config{MachineID: 1})
	event, _ := manager.CreateEvent(notify.ChannelEmail, EmailPayload{
		To:      "user@example.com",
		Subject: "Hello",
		Body:    "This is a test",
	})
	
	// Send to queue (implementation depends on your driver)
	// e.g., Redis: client.Push(event)
	// e.g., RabbitMQ: channel.Publish(event)
	// e.g., Kafka: producer.Send(event)
}
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   Application                           │
└─────────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────────┐
│              Middleware Chain (Optional)                 │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │  Logging MW  │→ │  Metrics MW  │→ │  RateLimit   │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────────┐
│                  Engine Core                            │
│  ┌─────────────────────────────────────────────────────┐│
│  │ • Event Validation & Expiration                     ││
│  │ • Automatic Retries (Exponential Backoff)           ││
│  │ • Provider Routing & Selection                      ││
│  │ • Circuit Breaker Management                        ││
│  │ • Dead Letter Queue Handling                        ││
│  └─────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────────┐
│              Provider Layer                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │  Email Prov  │  │  SMS Prov    │  │  Push Prov   │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────┘
                         ↓
                  External Services
```

## Core Concepts

### Driver

The `Driver` interface abstracts the message queue. It handles:
- **Listen**: Receiving notification events from the queue
- **Ack**: Acknowledging successful processing
- **Nack**: Requeuing failed events with backoff

```go
type Driver[T any] interface {
	Listen(ctx context.Context) (<-chan *NotificationEvent[T], error)
	Ack(eventID uint64) error
	Nack(event *NotificationEvent[T], delay time.Duration) error
}
```

### Provider

The `Provider` interface implements delivery to a specific channel:

```go
type Provider[T any] interface {
	Send(ctx context.Context, event *NotificationEvent[T]) error
	Type() string // "email", "sms", "push", etc.
	Ping(ctx context.Context) error
}
```

### Middleware

Middleware allows composing cross-cutting concerns without modifying core logic:

```go
type Middleware[T any] func(Handler[T]) Handler[T]
type Handler[T any] func(ctx context.Context, event *NotificationEvent[T]) error
```

## Using Middleware

### Built-in Middleware

The engine automatically includes `RecoveryMiddleware` for panic handling.

### Built-in Examples

Seven ready-to-use middleware implementations are provided:

#### Logging Middleware

```go
engine.UseMiddleware(
	notify.LoggingMiddleware[MyPayload](logger),
)
```

Provides debug-level logging at each stage of processing.

#### Metrics Middleware

```go
type MyMetricsCollector struct{}

func (m *MyMetricsCollector) RecordEventProcessed(
	duration time.Duration,
	channel string,
	success bool,
) {
	// Send to Prometheus, StatsD, etc.
}

engine.UseMiddleware(
	notify.MetricsMiddleware[MyPayload](myCollector),
)
```

#### Rate Limiting Middleware

```go
limiter := notify.NewChannelRateLimiter(
	100,                  // max events
	time.Minute,          // per duration
)

engine.UseMiddleware(
	notify.RateLimitMiddleware[MyPayload](limiter),
)
```

Implements token bucket rate limiting per channel. Transient failures when rate is exceeded.

#### Filtering Middleware

```go
filter := func(ctx context.Context, event *notify.NotificationEvent[MyPayload]) bool {
	// Skip notifications to "noreply@" addresses
	return !strings.HasPrefix(event.Payload.To, "noreply@")
}

engine.UseMiddleware(
	notify.FilterMiddleware(filter, logger),
)
```

#### Enrichment Middleware

```go
enricher := func(ctx context.Context, event *notify.NotificationEvent[MyPayload]) error {
	// Add metadata like request ID, user ID, etc.
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["request_id"] = getRequestID(ctx)
	return nil
}

engine.UseMiddleware(
	notify.EnrichmentMiddleware(enricher),
)
```

#### Timeout Middleware

```go
engine.UseMiddleware(
	notify.TimeoutMiddleware[MyPayload](5 * time.Second),
)
```

Enforces a maximum processing time per event.

#### Retry Middleware

```go
engine.UseMiddleware(
	notify.RetryMiddleware[MyPayload](notify.RetryConfig{
		MaxAttempts: 3,
		Backoff:     100 * time.Millisecond,
	}, logger),
)
```

**Note**: This is independent of the engine's built-in retry mechanism and runs at the middleware level.

#### Tracing Middleware

```go
engine.UseMiddleware(
	notify.TracingMiddleware[MyPayload](logger),
)
```

Adds trace context propagation for distributed tracing systems.

### Creating Custom Middleware

```go
func MyCustomMiddleware[T any]() notify.Middleware[T] {
	return func(next notify.Handler[T]) notify.Handler[T] {
		return func(ctx context.Context, event *notify.NotificationEvent[T]) error {
			// Pre-processing
			start := time.Now()
			
			// Call next middleware/handler
			err := next(ctx, event)
			
			// Post-processing
			duration := time.Since(start)
			if err != nil {
				log.Printf("Event %d failed after %v", event.EventID, duration)
			}
			
			return err
		}
	}
}

// Register it
engine.UseMiddleware(MyCustomMiddleware[MyPayload]())
```

### Composing Multiple Middleware

```go
engine.UseMiddleware(
	notify.LoggingMiddleware[MyPayload](logger),
	notify.MetricsMiddleware[MyPayload](metrics),
	notify.RateLimitMiddleware[MyPayload](limiter),
	notify.TimeoutMiddleware[MyPayload](5 * time.Second),
)
```

Middleware executes left-to-right: Logging → Metrics → RateLimit → Timeout → Handler

## Event Lifecycle

1. **Creation** - Event created with `Manager.CreateEvent()`
2. **Queue** - Event sent to Driver (message queue)
3. **Listen** - Engine listens to driver for events
4. **Validation** - Event structure and expiration checked
5. **Routing** - Provider selected based on channel
6. **Delivery** - Provider sends notification
7. **Acknowledgment** - On success, event Ack'd
8. **Retry/Requeue** - On failure, event Nack'd with backoff
9. **Dead Letter** - After max retries, sent to DLQ hook

## Error Handling

The engine distinguishes between error types:

```go
type ErrorType int

const (
	ErrorTransient    // Network timeout, 5xx errors - retry
	ErrorPermanent    // Invalid API key, malformed payload - drop
	ErrorInvalidData  // Validation failure - drop
)
```

Transient errors trigger retries. Permanent and invalid data errors are dropped.

## Circuit Breaker

Providers are automatically wrapped with a circuit breaker:

- **Closed State** - Requests pass through normally
- **Open State** - Requests fail immediately after threshold breaches
- **Half-Open State** - Single probe request to test recovery

```
Failures → Threshold → Open → Cooldown → Half-Open → Probe → Closed/Open
```

The circuit breaker recovers automatically with configurable cooldown (default: 30s).

## Configuration Patterns

### Builder Pattern (Recommended for Phase 2)

```go
// Future enhancement
engine := notify.NewEngineBuilder(driver).
	WithProviders(providers).
	WithWorkers(8).
	WithLogger(logger).
	WithDeadLetterHook(dlh).
	WithMiddleware(
		notify.LoggingMiddleware(logger),
		notify.MetricsMiddleware(metrics),
	).
	Build()
```

### Current Configuration

```go
engine, err := notify.NewEngine(
	driver,                    // Message queue
	providers,                 // Channel providers
	4,                         // Worker count
	slog.Default(),            // Logger
	deadLetterHook,            // Handle failures
)

// Then add middleware
engine.UseMiddleware(middlewares...)

// Start processing
ctx := context.Background()
go engine.Start(ctx)
```

## Best Practices

### 1. Use Structured Logging

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
engine, _ := notify.NewEngine(driver, providers, 4, logger, nil)
```

### 2. Implement Proper Dead Letter Handling

```go
type DLQHandler struct {
	db *sql.DB
}

func (h *DLQHandler) Handle(event *notify.NotificationEvent[T], err error) error {
	// Log to database for later analysis
	_, execErr := h.db.Exec(
		"INSERT INTO dlq (event_id, channel, error, timestamp) VALUES (?, ?, ?, ?)",
		event.EventID, event.Channel, err.Error(), time.Now(),
	)
	return execErr
}

engine, _ := notify.NewEngine(driver, providers, 4, logger, &DLQHandler{db})
```

### 3. Graceful Shutdown

```go
ctx, cancel := context.WithCancel(context.Background())
go engine.Start(ctx)

// On interrupt signal
<-sigChan
cancel() // Stops processing, allows in-flight events to complete
```

### 4. Tune Worker Count

```go
// Default: runtime.NumCPU()
workers := runtime.NumCPU() * 2 // For I/O bound providers
engine, _ := notify.NewEngine(driver, providers, workers, logger, dlh)
```

### 5. Monitor Circuit Breaker Health

```go
// Implement custom observability
engine.UseMiddleware(func(next notify.Handler[T]) notify.Handler[T] {
	return func(ctx context.Context, event *notify.NotificationEvent[T]) error {
		err := next(ctx, event)
		if err != nil {
			metrics.IncrementProviderError(string(event.Channel), err)
		}
		return err
	}
})
```

## Performance

Benchmarks on a modern CPU:

| Benchmark | Ops | Per-Op | Memory |
|-----------|-----|--------|--------|
| Engine Processing | 25.7M | **44.09 ns/op** | 1 B/op, 0 allocs |
| Saturation Recovery | 5.7K | 177.0 µs/op | 8.6 KB/op, 180 allocs |

The middleware system adds minimal overhead - composition happens at startup, not runtime.

## Testing

Run the full test suite:

```bash
go test ./pkg/notify/... -v
```

Run benchmarks:

```bash
go test ./pkg/notify/... -bench=. -benchmem
```

Run with coverage:

```bash
go test ./pkg/notify/... -cover
```

## Example: Email Notification System

```go
package main

import (
	"context"
	"log/slog"
	"net/smtp"
	"time"

	"github.com/expoR93/go-notify/pkg/notify"
)

type EmailPayload struct {
	To      string
	Subject string
	Body    string
}

// Implement notify.Provider[EmailPayload]
type EmailProvider struct {
	smtpHost string
	smtpPort string
}

func (ep *EmailProvider) Send(ctx context.Context, event *notify.NotificationEvent[EmailPayload]) error {
	auth := smtp.PlainAuth("", "user@example.com", "password", ep.smtpHost)
	
	msg := []byte("To: " + event.Payload.To + "\r\n" +
		"Subject: " + event.Payload.Subject + "\r\n" +
		"\r\n" +
		event.Payload.Body)
	
	addr := ep.smtpHost + ":" + ep.smtpPort
	err := smtp.SendMail(addr, auth, "sender@example.com", []string{event.Payload.To}, msg)
	
	if err != nil {
		// Determine if error is permanent or transient
		if isPermanent(err) {
			return &notify.ProviderError{Err: err, Type: notify.ErrorPermanent}
		}
		return &notify.ProviderError{Err: err, Type: notify.ErrorTransient}
	}
	
	return nil
}

func (ep *EmailProvider) Type() string {
	return "email"
}

func (ep *EmailProvider) Ping(ctx context.Context) error {
	// Check SMTP connectivity
	return nil
}

func main() {
	// Setup
	logger := slog.Default()
	provider := &EmailProvider{smtpHost: "smtp.gmail.com", smtpPort: "587"}
	
	// Create driver (implement notify.Driver interface)
	// driver := NewRedisDriver() or similar
	
	// Create engine
	engine, _ := notify.NewEngine(
		driver,
		[]notify.Provider[EmailPayload]{provider},
		4,
		logger,
		nil,
	)
	
	// Add middleware
	engine.UseMiddleware(
		notify.LoggingMiddleware[EmailPayload](logger),
		notify.RateLimitMiddleware[EmailPayload](
			notify.NewChannelRateLimiter(1000, time.Minute),
		),
	)
	
	// Start
	ctx := context.Background()
	go engine.Start(ctx)
	
	// Create and queue an event
	manager := notify.NewManager[EmailPayload](notify.Config{MachineID: 1})
	event, _ := manager.CreateEvent(notify.ChannelEmail, EmailPayload{
		To:      "user@example.com",
		Subject: "Welcome!",
		Body:    "Thanks for signing up.",
	})
	
	// Send via driver (implementation dependent)
	// driver.Queue(event)
}

func isPermanent(err error) bool {
	// Check for permanent error indicators
	errStr := err.Error()
	return contains(errStr, "invalid address") || 
	       contains(errStr, "authentication failed")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes with tests
4. Run `go test ./...` to ensure all tests pass
5. Run `go fmt ./...` to format code
6. Commit with descriptive messages
7. Push to the branch
8. Open a Pull Request

## License

MIT License - see LICENSE file for details

## Roadmap

- [ ] Builder pattern for engine configuration
- [ ] More specific error types
- [ ] Built-in observability hooks
- [ ] Example providers (Email, SMS, Push)
- [ ] Health check endpoints
- [ ] Prometheus metrics exporter
- [ ] Grafana dashboard templates
- [ ] Admin API for queue management
- [ ] Event filtering and routing rules
- [ ] Persistence and recovery guarantees

## Support

For issues, questions, or suggestions, please open an issue on GitHub.

---

**Built with ❤️ in Go**