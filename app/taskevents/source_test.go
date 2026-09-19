package taskevents

import (
	"context"
	"testing"
	"time"

	"cosmossdk.io/log"
	clientmocks "github.com/cometbft/cometbft/rpc/client/mocks"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCometSourceStopsWhenNodeContextIsCancelled(t *testing.T) {
	client := &clientmocks.Client{}
	newBlocks := make(chan coretypes.ResultEvent)
	statusCalled := make(chan struct{})
	client.On("Subscribe", mock.Anything, "task-event-grpc", newBlockQuery, 64).Return((<-chan coretypes.ResultEvent)(newBlocks), nil).Once()
	client.On("Status", mock.Anything).Run(func(mock.Arguments) {
		close(statusCalled)
	}).Return(&coretypes.ResultStatus{SyncInfo: coretypes.SyncInfo{LatestBlockHeight: 10}}, nil).Once()
	client.On("Unsubscribe", mock.Anything, "task-event-grpc", newBlockQuery).Return(nil).Once()

	source := newCometSource(log.NewNopLogger(), 100, newBroker(1, 1))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx, client) }()
	<-statusCalled
	cancel()
	require.NoError(t, <-done)
	client.AssertExpectations(t)
}

func TestCometSourceIsNotReadyBeforeSubscriptionBaseline(t *testing.T) {
	client := &clientmocks.Client{}
	newBlocks := make(chan coretypes.ResultEvent)
	statusEntered := make(chan struct{})
	releaseStatus := make(chan struct{})
	client.On("Subscribe", mock.Anything, "task-event-grpc", newBlockQuery, 64).Return((<-chan coretypes.ResultEvent)(newBlocks), nil).Once()
	client.On("Status", mock.Anything).Run(func(mock.Arguments) {
		close(statusEntered)
		<-releaseStatus
	}).Return(&coretypes.ResultStatus{SyncInfo: coretypes.SyncInfo{LatestBlockHeight: 10}}, nil).Once()
	client.On("Status", mock.Anything).Return(&coretypes.ResultStatus{SyncInfo: coretypes.SyncInfo{LatestBlockHeight: 10}}, nil).Once()
	client.On("Unsubscribe", mock.Anything, "task-event-grpc", newBlockQuery).Return(nil).Once()

	source := newCometSource(log.NewNopLogger(), 100, newBroker(1, 1))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx, client) }()
	<-statusEntered

	latest := make(chan uint64, 1)
	go func() {
		height, _ := source.LatestHeight(ctx)
		latest <- height
	}()
	select {
	case <-latest:
		t.Fatal("source became ready before its initial subscription baseline")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseStatus)
	require.Equal(t, uint64(10), <-latest)
	cancel()
	require.NoError(t, <-done)
	client.AssertExpectations(t)
}

func TestCometSourceCatchUpDoesNotTruncateAtClientReplayLimit(t *testing.T) {
	client := &clientmocks.Client{}
	for height := int64(1); height <= 3; height++ {
		expectedHeight := height
		heightMatcher := mock.MatchedBy(func(value *int64) bool {
			return value != nil && *value == expectedHeight
		})
		client.On("Block", mock.Anything, heightMatcher).Return(&coretypes.ResultBlock{
			Block: &cmttypes.Block{Header: cmttypes.Header{Height: height}},
		}, nil).Once()
		client.On("BlockResults", mock.Anything, heightMatcher).Return(&coretypes.ResultBlockResults{Height: height}, nil).Once()
	}

	broker := newBroker(1, 1)
	source := newCometSource(log.NewNopLogger(), 2, broker)
	source.client = client
	close(source.ready)
	lastHeight, err := source.publishRange(context.Background(), 1, 3)
	require.NoError(t, err)
	require.Equal(t, int64(3), lastHeight)
	require.Equal(t, uint64(3), broker.latestHeight)
	client.AssertExpectations(t)
}
