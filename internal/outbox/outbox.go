// Package outbox implements the transactional outbox pattern. Events are
// written to outbox_messages in the same transaction as the state change that
// produced them, then a relay publishes them at-least-once and marks them sent.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	return db.New(tx).EnqueueOutboxMessage(ctx, aggregateID, eventType, payload)
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

// maxErrorBytes bounds the publisher error stored in last_error.
const maxErrorBytes = 2048

// storableError turns a publisher error into text Postgres always accepts. A
// TEXT column rejects NUL bytes and invalid UTF-8, which a broker error that
// echoes payload bytes can contain, and a failed RecordOutboxFailure rolls back
// the whole batch: the messages already published would be sent again on every
// tick while the rejected one never backs off.
func storableError(err error) string {
	msg := strings.ReplaceAll(err.Error(), "\x00", "")
	if len(msg) > maxErrorBytes {
		msg = msg[:maxErrorBytes]
	}
	return strings.ToValidUTF8(msg, "?")
}

// purgeBatchSize bounds how many rows one purge statement deletes, keeping
// each delete transaction (and its locks and WAL) short.
const purgeBatchSize = 5000

// Run polls until the context is cancelled. Each tick drains the backlog: it
// keeps fetching batches while they come back full and fully published, so
// throughput is not capped at one batch per interval.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.drain(ctx)
		}
	}
}

func (r *Relay) drain(ctx context.Context) {
	total := 0
	for ctx.Err() == nil {
		n, err := r.ProcessBatch(ctx)
		total += n
		if err != nil {
			r.logger.ErrorContext(ctx, "outbox relay batch failed", slog.Any("error", err))
			break
		}
		// n == 0 also ends the drain when batchSize is not positive, where
		// n < batchSize never holds and the loop would spin without pausing.
		if n == 0 || n < int(r.batchSize) {
			break
		}
	}
	if total > 0 {
		r.logger.DebugContext(ctx, "outbox relay published", slog.Int("count", total))
	}
}

// PurgePublished deletes already-published messages older than retention and
// returns how many were removed, keeping the table and its index bounded. It
// deletes in bounded batches so a large backlog never becomes one huge
// transaction.
func (r *Relay) PurgePublished(ctx context.Context, retention time.Duration) (int64, error) {
	before := time.Now().Add(-retention)
	queries := db.New(r.pool)
	var total int64
	for {
		n, err := queries.DeletePublishedOutboxBefore(ctx, before, purgeBatchSize)
		total += n
		if err != nil {
			return total, fmt.Errorf("purge published outbox messages: %w", err)
		}
		if n < purgeBatchSize {
			return total, nil
		}
	}
}

// ProcessBatch publishes one batch within a transaction and returns how many
// messages it published. Rows are locked with FOR UPDATE SKIP LOCKED so
// multiple relay instances do not collide.
//
// A message the publisher rejects does not stop the batch: its attempt and
// error are recorded and it backs off before it is fetched again, while the
// messages around it are published and marked as usual. The rejections are
// returned, joined, after the batch commits. Because a rejected message is
// retried later, delivery order is not guaranteed.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	var (
		published int
		failures  []error
	)
	err := database.WithinTx(ctx, r.pool, func(tx pgx.Tx) error {
		published, failures = 0, nil
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
			pubErr := r.publisher.Publish(ctx, Message{
				ID:          row.ID,
				AggregateID: row.AggregateID,
				EventType:   row.EventType,
				Payload:     row.Payload,
			})
			if pubErr == nil {
				ids = append(ids, row.ID)
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				// Shutting down: the rejection says nothing about the message.
				return ctxErr
			}
			attempts, err := queries.RecordOutboxFailure(ctx, storableError(pubErr), row.ID)
			if err != nil {
				return err
			}
			failures = append(failures, fmt.Errorf("publish outbox message %d (attempt %d): %w", row.ID, attempts, pubErr))
		}
		published = len(ids)
		if published == 0 {
			return nil
		}
		return queries.MarkOutboxPublished(ctx, ids)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, nil
		}
		return 0, err
	}
	return published, errors.Join(failures...)
}
