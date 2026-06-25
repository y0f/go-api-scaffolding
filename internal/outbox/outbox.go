// Package outbox implements the transactional outbox pattern. Events are
// written to outbox_messages in the same transaction as the state change that
// produced them, then a relay publishes them at-least-once and marks them sent.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/y0f/go-api-scaffolding/internal/platform/database"

	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/y0f/go-api-scaffolding/internal/gen/db"
)

// Message is a domain event awaiting publication.
type Message struct {
	ID          int64
	AggregateID uuid.UUID
	EventType   string
	Payload     []byte
}

// Publisher delivers a message to the outside world (a broker, webhook, etc.).
// Delivery is at-least-once, so consumers must be idempotent.
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}

// LogPublisher is the default Publisher. It records the event and is meant to
// be replaced with a real broker (NATS, Kafka) in a deployed service.
type LogPublisher struct{ Logger *slog.Logger }

func (p LogPublisher) Publish(ctx context.Context, msg Message) error {
	p.Logger.InfoContext(ctx, "outbox event published",
		slog.String("event_type", msg.EventType),
		slog.String("aggregate_id", msg.AggregateID.String()),
	)
	return nil
}

// Enqueue writes an event inside an existing transaction. Call it from the same
// tx that persists the aggregate change.
func Enqueue(ctx context.Context, tx pgx.Tx, aggregateID uuid.UUID, eventType string, payload []byte) error {
	_, err := db.New(tx).EnqueueOutboxMessage(ctx, aggregateID, eventType, payload)
	return err
}

// Relay polls for unpublished messages and publishes them.
type Relay struct {
	pool      *pgxpool.Pool
	publisher Publisher
	logger    *slog.Logger
	batchSize int32
	interval  time.Duration
}

func NewRelay(pool *pgxpool.Pool, publisher Publisher, logger *slog.Logger, batchSize int32, interval time.Duration) *Relay {
	return &Relay{pool: pool, publisher: publisher, logger: logger, batchSize: batchSize, interval: interval}
}

// Run polls until the context is cancelled. Each tick drains up to one batch.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if n, err := r.ProcessBatch(ctx); err != nil {
				r.logger.ErrorContext(ctx, "outbox relay batch failed", slog.Any("error", err))
			} else if n > 0 {
				r.logger.DebugContext(ctx, "outbox relay published batch", slog.Int("count", n))
			}
		}
	}
}

// PurgePublished deletes already-published messages older than retention and
// returns how many were removed, keeping the table and its index bounded.
func (r *Relay) PurgePublished(ctx context.Context, retention time.Duration) (int64, error) {
	n, err := db.New(r.pool).DeletePublishedOutboxBefore(ctx, time.Now().Add(-retention))
	if err != nil {
		return 0, fmt.Errorf("purge published outbox messages: %w", err)
	}
	return n, nil
}

// ProcessBatch publishes one batch within a transaction. Rows are locked with
// FOR UPDATE SKIP LOCKED so multiple relay instances do not collide.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	var published int
	err := database.WithinTx(ctx, r.pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		rows, err := queries.FetchUnpublishedOutbox(ctx, r.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			if err := r.publisher.Publish(ctx, Message{
				ID:          row.ID,
				AggregateID: row.AggregateID,
				EventType:   row.EventType,
				Payload:     row.Payload,
			}); err != nil {
				return err
			}
			ids = append(ids, row.ID)
		}
		published = len(ids)
		return queries.MarkOutboxPublished(ctx, ids)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, nil
		}
		return 0, err
	}
	return published, nil
}
