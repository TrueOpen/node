package taskevents

import (
	"context"
	"errors"
	"sync"
)

var ErrMaxSubscribers = errors.New("task event subscriber limit reached")
var ErrSlowSubscriber = errors.New("task event subscriber buffer is full")
var ErrBrokerClosed = errors.New("task event broker is closed")

type eventFilter func(chainEvent) bool

type subscription struct {
	id       uint64
	events   chan chainEvent
	terminal chan error
	filter   eventFilter
}

type broker struct {
	mu               sync.Mutex
	maxSubscribers   int
	subscriberBuffer int
	nextID           uint64
	latestHeight     uint64
	closed           bool
	subscribers      map[uint64]*subscription
}

func newBroker(maxSubscribers, subscriberBuffer int) *broker {
	return &broker{
		maxSubscribers:   maxSubscribers,
		subscriberBuffer: subscriberBuffer,
		subscribers:      make(map[uint64]*subscription),
	}
}

// PublishBlock makes delivery and latest-height advancement atomic so a new
// subscriber cannot land in the middle of a committed block.
func (b *broker) PublishBlock(height uint64, events []chainEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if height <= b.latestHeight {
		return
	}
	for _, event := range events {
		for id, subscriber := range b.subscribers {
			if !subscriber.filter(event) {
				continue
			}
			select {
			case subscriber.events <- event:
			default:
				delete(b.subscribers, id)
				subscriber.terminal <- ErrSlowSubscriber
			}
		}
	}
	checkpoint := blockCheckpoint(height)
	for _, subscriber := range b.subscribers {
		select {
		case subscriber.events <- checkpoint:
		default:
			// Checkpoints are resumability hints and can be reconstructed from
			// later blocks. A full business-event queue must not be disconnected
			// solely because its optional checkpoint has no room.
		}
	}
	b.latestHeight = height
}

func (b *broker) Subscribe(filter eventFilter) (*subscription, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, 0, ErrBrokerClosed
	}
	if len(b.subscribers) >= b.maxSubscribers {
		return nil, 0, ErrMaxSubscribers
	}
	b.nextID++
	// Keep one checkpoint slot outside the configured business-event buffer so
	// a single matching event cannot make a healthy minimum-size stream fail.
	eventBufferSize := b.subscriberBuffer + 1
	subscriber := &subscription{
		id:       b.nextID,
		events:   make(chan chainEvent, eventBufferSize),
		terminal: make(chan error, 1),
		filter:   filter,
	}
	b.subscribers[subscriber.id] = subscriber
	return subscriber, b.latestHeight, nil
}

func (b *broker) Unsubscribe(subscriber *subscription) {
	if subscriber == nil {
		return
	}
	b.mu.Lock()
	delete(b.subscribers, subscriber.id)
	b.mu.Unlock()
}

// Disconnect ends every current stream without closing the broker. The source
// uses this on an upstream interruption so clients observe the gap and resume
// from their last cursor after reconnecting.
func (b *broker) Disconnect(err error) {
	if err == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for id, subscriber := range b.subscribers {
		delete(b.subscribers, id)
		subscriber.terminal <- err
	}
}

func (b *broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, subscriber := range b.subscribers {
		delete(b.subscribers, id)
		subscriber.terminal <- context.Canceled
	}
}
