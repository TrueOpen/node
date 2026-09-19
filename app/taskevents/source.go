package taskevents

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"cosmossdk.io/log"
	cmtrpc "github.com/cometbft/cometbft/rpc/client"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
)

const newBlockQuery = "tm.event='NewBlock'"

type eventSink interface {
	PublishBlock(uint64, []chainEvent)
	Disconnect(error)
}

var ErrReplayRangeExceeded = errors.New("requested replay range exceeds max-replay-blocks")

type cometSource struct {
	logger          log.Logger
	maxReplayBlocks uint64
	sink            eventSink

	mu     sync.RWMutex
	client cmtrpc.Client
	ready  chan struct{}
	once   sync.Once
}

func newCometSource(logger log.Logger, maxReplayBlocks uint64, sink eventSink) *cometSource {
	return &cometSource{logger: logger, maxReplayBlocks: maxReplayBlocks, sink: sink, ready: make(chan struct{})}
}

func (s *cometSource) Run(ctx context.Context, client cmtrpc.Client) error {
	if client == nil {
		return fmt.Errorf("task event service requires a CometBFT client")
	}
	s.mu.Lock()
	s.client = client
	s.mu.Unlock()

	var lastHeight int64
	for ctx.Err() == nil {
		height, err := s.runSubscription(ctx, client, lastHeight)
		if height > lastHeight {
			lastHeight = height
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			s.logger.Error("task event CometBFT subscription interrupted", "height", lastHeight, "err", err)
			select {
			case <-s.ready:
				s.sink.Disconnect(fmt.Errorf("task event source interrupted: %w", err))
			default:
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func (s *cometSource) runSubscription(ctx context.Context, client cmtrpc.Client, lastHeight int64) (int64, error) {
	subscriber := "task-event-grpc"
	events, err := client.Subscribe(ctx, subscriber, newBlockQuery, 64)
	if err != nil {
		return lastHeight, err
	}
	defer func() {
		unsubscribeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Unsubscribe(unsubscribeCtx, subscriber, newBlockQuery)
	}()

	status, err := client.Status(ctx)
	if err != nil {
		return lastHeight, err
	}
	latest := status.SyncInfo.LatestBlockHeight
	if lastHeight == 0 {
		lastHeight = latest
		if latest > 0 {
			s.sink.PublishBlock(uint64(latest), nil)
		}
	} else if latest > lastHeight {
		lastHeight, err = s.publishRange(ctx, lastHeight+1, latest)
		if err != nil {
			return lastHeight, err
		}
	}
	// Only expose the source after the subscription and its initial height
	// baseline are both established. This prevents a startup replay/live gap.
	s.once.Do(func() { close(s.ready) })

	for {
		select {
		case <-ctx.Done():
			return lastHeight, nil
		case event := <-events:
			height, err := newBlockHeight(event)
			if err != nil {
				return lastHeight, err
			}
			if height <= lastHeight {
				continue
			}
			lastHeight, err = s.publishRange(ctx, lastHeight+1, height)
			if err != nil {
				return lastHeight, err
			}
		}
	}
}

func (s *cometSource) publishRange(ctx context.Context, fromHeight, toHeight int64) (int64, error) {
	if toHeight < fromHeight {
		return toHeight, nil
	}
	lastHeight := fromHeight - 1
	for height := fromHeight; height <= toHeight; height++ {
		events, err := s.eventsAtHeight(ctx, height)
		if err != nil {
			return lastHeight, err
		}
		s.sink.PublishBlock(uint64(height), events)
		lastHeight = height
	}
	return lastHeight, nil
}

func (s *cometSource) Replay(
	ctx context.Context,
	fromHeight, toHeight uint64,
	after *eventPosition,
	filter eventFilter,
	send func(chainEvent) error,
) error {
	if fromHeight == 0 || toHeight < fromHeight {
		return nil
	}
	if toHeight-fromHeight+1 > s.maxReplayBlocks {
		return fmt.Errorf("%w %d", ErrReplayRangeExceeded, s.maxReplayBlocks)
	}
	for height := fromHeight; height <= toHeight; height++ {
		events, err := s.eventsAtHeight(ctx, int64(height))
		if err != nil {
			return err
		}
		for _, event := range events {
			if after != nil && comparePosition(event.position(), *after) <= 0 {
				continue
			}
			if filter(event) {
				if err := send(event); err != nil {
					return err
				}
			}
		}
	}
	checkpoint := blockCheckpoint(toHeight)
	if after == nil || comparePosition(checkpoint.position(), *after) > 0 {
		if err := send(checkpoint); err != nil {
			return err
		}
	}
	return nil
}

func (s *cometSource) LatestHeight(ctx context.Context) (uint64, error) {
	client, err := s.waitForClient(ctx)
	if err != nil {
		return 0, err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return 0, err
	}
	if status.SyncInfo.LatestBlockHeight < 0 {
		return 0, fmt.Errorf("CometBFT returned a negative latest height")
	}
	return uint64(status.SyncInfo.LatestBlockHeight), nil
}

func (s *cometSource) eventsAtHeight(ctx context.Context, height int64) ([]chainEvent, error) {
	client, err := s.waitForClient(ctx)
	if err != nil {
		return nil, err
	}
	block, err := client.Block(ctx, &height)
	if err != nil {
		return nil, fmt.Errorf("load block %d: %w", height, err)
	}
	results, err := client.BlockResults(ctx, &height)
	if err != nil {
		return nil, fmt.Errorf("load block results %d: %w", height, err)
	}
	if block == nil || block.Block == nil || results == nil {
		return nil, fmt.Errorf("incomplete block data at height %d", height)
	}
	blockEvents := blockData{
		Height:              height,
		BlockHash:           block.BlockID.Hash.Bytes(),
		Txs:                 block.Block.Data.Txs,
		TxResults:           results.TxsResults,
		FinalizeBlockEvents: results.FinalizeBlockEvents,
	}
	return eventsFromBlockObserved(blockEvents, func(eventType string, source string, txIndex uint32, eventIndex uint32) {
		s.logger.Error(
			"skipping malformed or unsupported TrueOpen typed event",
			"height", height,
			"source", source,
			"tx_index", txIndex,
			"event_index", eventIndex,
			"event_type", eventType,
		)
	}), nil
}

func (s *cometSource) waitForClient(ctx context.Context) (cmtrpc.Client, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ready:
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client == nil {
		return nil, errors.New("task event source is not ready")
	}
	return s.client, nil
}

func newBlockHeight(event coretypes.ResultEvent) (int64, error) {
	switch data := event.Data.(type) {
	case cmttypes.EventDataNewBlock:
		if data.Block != nil {
			return data.Block.Height, nil
		}
	case *cmttypes.EventDataNewBlock:
		if data != nil && data.Block != nil {
			return data.Block.Height, nil
		}
	}
	if values := event.Events["block.height"]; len(values) > 0 {
		height, err := strconv.ParseInt(values[0], 10, 64)
		if err == nil {
			return height, nil
		}
	}
	return 0, fmt.Errorf("NewBlock event does not contain a block height")
}
