package mcp

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// EventType identifies a category of knowledge base change.
type EventType string

// Supported event types for subscription notifications.
const (
	EventKnowledgeUpdated EventType = "knowledge.updated"
	EventScanCompleted    EventType = "scan.completed"
	EventRuleAdded        EventType = "rule.added"
	EventModuleChanged    EventType = "module.changed"
)

// Event carries information about a knowledge base change.
type Event struct {
	// Type identifies the category of change.
	Type EventType `json:"type"`
	// Detail provides human-readable context about the event.
	Detail string `json:"detail,omitempty"`
}

// EventHandler is a callback invoked when a subscribed event occurs.
// Handlers must not block — they are called synchronously during fan-out.
type EventHandler func(Event)

// UnsubscribeFunc removes a subscription when called. It is safe to call
// multiple times; subsequent calls are no-ops.
type UnsubscribeFunc func()

// subscription tracks a single handler registration.
type subscription struct {
	id      uint64
	event   EventType
	handler EventHandler
}

// Broker manages event subscriptions and fan-out.
//
// It is safe for concurrent use: multiple goroutines may subscribe,
// unsubscribe, and publish simultaneously. Subscriptions are not persisted —
// they last only for the lifetime of the MCP connection. This is intentional:
// agents reconnect frequently, and stale subscriptions would leak goroutines.
type Broker struct {
	mu           sync.RWMutex
	subs         map[uint64]subscription
	nextID       atomic.Uint64
	byEvent      map[EventType][]uint64
	unsubscribed map[uint64]bool
}

// NewBroker creates a ready-to-use event broker.
func NewBroker() *Broker {
	return &Broker{
		subs:         make(map[uint64]subscription),
		byEvent:      make(map[EventType][]uint64),
		unsubscribed: make(map[uint64]bool),
	}
}

// Subscribe registers a handler for the given event type. It returns an
// UnsubscribeFunc that the caller must invoke to release resources. The
// returned function is safe to call multiple times.
//
// It returns an error if the event type is empty.
func (b *Broker) Subscribe(event EventType, handler EventHandler) (UnsubscribeFunc, error) {
	if event == "" {
		return nil, fmt.Errorf("mcp: subscribe: event type must not be empty")
	}
	if handler == nil {
		return nil, fmt.Errorf("mcp: subscribe: handler must not be nil")
	}

	id := b.nextID.Add(1)

	b.mu.Lock()
	b.subs[id] = subscription{id: id, event: event, handler: handler}
	b.byEvent[event] = append(b.byEvent[event], id)
	b.mu.Unlock()

	once := sync.Once{}
	return func() {
		once.Do(func() {
			b.unsubscribe(id, event)
		})
	}, nil
}

// unsubscribe removes a subscription by ID.
func (b *Broker) unsubscribe(id uint64, event EventType) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.subs, id)
	b.unsubscribed[id] = true

	// Remove from byEvent index.
	ids := b.byEvent[event]
	for i, sid := range ids {
		if sid == id {
			b.byEvent[event] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
}

// Publish sends an event to all handlers subscribed to that event type.
// Handlers are invoked synchronously in subscription order. A panicking
// handler is recovered and does not affect other subscribers.
func (b *Broker) Publish(event Event) {
	b.mu.RLock()
	ids := make([]uint64, len(b.byEvent[event.Type]))
	copy(ids, b.byEvent[event.Type])
	b.mu.RUnlock()

	for _, id := range ids {
		b.mu.RLock()
		sub, ok := b.subs[id]
		b.mu.RUnlock()

		if !ok {
			continue
		}
		b.safeCall(sub.handler, event)
	}
}

// SubscriberCount returns the number of active subscriptions for the given
// event type, or the total if event is empty.
func (b *Broker) SubscriberCount(event EventType) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if event == "" {
		return len(b.subs)
	}

	count := 0
	for _, id := range b.byEvent[event] {
		if _, ok := b.subs[id]; ok {
			count++
		}
	}
	return count
}

// safeCall invokes a handler, recovering from panics to prevent one
// misbehaving subscriber from affecting others.
func (b *Broker) safeCall(handler EventHandler, event Event) {
	defer func() {
		recover() //nolint:errcheck // intentionally swallowing panics from handlers.
	}()
	handler(event)
}
