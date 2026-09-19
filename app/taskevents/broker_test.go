package taskevents

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrokerDisconnectsSlowSubscriberWithoutBlockingOthers(t *testing.T) {
	broker := newBroker(2, 1)
	slow, _, err := broker.Subscribe(func(chainEvent) bool { return true })
	require.NoError(t, err)
	fast, _, err := broker.Subscribe(func(chainEvent) bool { return true })
	require.NoError(t, err)

	broker.PublishBlock(1, []chainEvent{{Height: 1}})
	require.Equal(t, uint64(1), (<-fast.events).Height)
	require.Equal(t, eventKindCheckpoint, (<-fast.events).Kind)
	broker.PublishBlock(2, []chainEvent{{Height: 2}})
	require.ErrorIs(t, <-slow.terminal, ErrSlowSubscriber)
	require.Equal(t, uint64(2), (<-fast.events).Height)
	require.Equal(t, eventKindCheckpoint, (<-fast.events).Kind)
}

func TestBrokerCloseTerminatesSubscribers(t *testing.T) {
	broker := newBroker(1, 1)
	subscriber, _, err := broker.Subscribe(func(chainEvent) bool { return true })
	require.NoError(t, err)
	broker.Close()
	require.ErrorIs(t, <-subscriber.terminal, context.Canceled)
	broker.Close()
}

func TestBrokerEnforcesSubscriberLimit(t *testing.T) {
	broker := newBroker(1, 1)
	_, _, err := broker.Subscribe(func(chainEvent) bool { return true })
	require.NoError(t, err)
	_, _, err = broker.Subscribe(func(chainEvent) bool { return true })
	require.ErrorIs(t, err, ErrMaxSubscribers)
}

func TestBrokerRejectsSubscriptionsAfterClose(t *testing.T) {
	broker := newBroker(1, 1)
	broker.Close()

	_, _, err := broker.Subscribe(func(chainEvent) bool { return true })
	require.ErrorIs(t, err, ErrBrokerClosed)
	broker.PublishBlock(1, []chainEvent{{Height: 1}})
	require.Zero(t, broker.latestHeight)
}

func TestBrokerDisconnectsCurrentStreamsButRemainsOpen(t *testing.T) {
	broker := newBroker(1, 1)
	first, _, err := broker.Subscribe(func(chainEvent) bool { return true })
	require.NoError(t, err)
	upstreamErr := errors.New("upstream interrupted")
	broker.Disconnect(upstreamErr)
	require.ErrorIs(t, <-first.terminal, upstreamErr)

	_, _, err = broker.Subscribe(func(chainEvent) bool { return true })
	require.NoError(t, err)
}
