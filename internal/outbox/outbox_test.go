//go:build integration

package outbox

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/y0f/go-api-scaffolding/internal/platform/database"
	"github.com/y0f/go-api-scaffolding/internal/testutil"
)

type capturePublisher struct {
	messages []Message
}

func (c *capturePublisher) Publish(_ context.Context, msg Message) error {
	c.messages = append(c.messages, msg)
	return nil
}

// flakyPublisher fails its first failUntil calls, then succeeds.
type flakyPublisher struct {
	failUntil int
	calls     int
}

func (f *flakyPublisher) Publish(context.Context, Message) error {
	f.calls++
	if f.calls <= f.failUntil {
		return errors.New("publish failed")
	}
	return nil
}

func TestRelayPublishesThenDrains(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	ctx := context.Background()

	err := database.WithinTx(ctx, pool, func(tx pgx.Tx) error {
		return Enqueue(ctx, tx, uuid.New(), "thing.created", []byte(`{"hello":"world"}`))
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	publisher := &capturePublisher{}
	relay := NewRelay(pool, publisher, slog.Default(), 10, time.Second)

	published, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if published != 1 || len(publisher.messages) != 1 {
		t.Fatalf("published %d, want 1", published)
	}
	if publisher.messages[0].EventType != "thing.created" {
		t.Errorf("event type = %q, want thing.created", publisher.messages[0].EventType)
	}

	published, err = relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if published != 0 {
		t.Errorf("second batch published %d, want 0", published)
	}
}

func TestRelayRedeliversAfterPublishFailure(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	ctx := context.Background()

	err := database.WithinTx(ctx, pool, func(tx pgx.Tx) error {
		return Enqueue(ctx, tx, uuid.New(), "thing.created", []byte(`{}`))
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The first publish fails, so the message stays unpublished and backs off;
	// once its retry time arrives the relay must redeliver it at-least-once.
	pub := &flakyPublisher{failUntil: 1}
	relay := NewRelay(pool, pub, slog.Default(), 10, time.Second)

	if _, err := relay.ProcessBatch(ctx); err == nil {
		t.Fatal("expected the first batch to report the failed publish")
	}
	if n, err := relay.ProcessBatch(ctx); err != nil || n != 0 {
		t.Fatalf("batch during back-off published %d (err %v), want 0", n, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE outbox_messages SET next_attempt_at = now()"); err != nil {
		t.Fatalf("expire back-off: %v", err)
	}

	published, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if published != 1 {
		t.Fatalf("redelivered %d, want 1", published)
	}
}

func TestPurgePublishedRemovesSentMessages(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	ctx := context.Background()

	err := database.WithinTx(ctx, pool, func(tx pgx.Tx) error {
		return Enqueue(ctx, tx, uuid.New(), "thing.created", []byte(`{}`))
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	relay := NewRelay(pool, &capturePublisher{}, slog.Default(), 10, time.Second)
	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatalf("process: %v", err)
	}

	// A negative retention puts the cutoff a minute in the future, so it covers
	// the message just published regardless of small client/DB clock differences.
	removed, err := relay.PurgePublished(ctx, -time.Minute)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if removed != 1 {
		t.Fatalf("purged %d, want 1", removed)
	}
}

func enqueueN(t *testing.T, pool *pgxpool.Pool, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	for range n {
		if err := database.WithinTx(ctx, pool, func(tx pgx.Tx) error {
			return Enqueue(ctx, tx, uuid.New(), "thing.created", []byte(`{}`))
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	rows, err := pool.Query(ctx, "SELECT id FROM outbox_messages ORDER BY id")
	if err != nil {
		t.Fatalf("list ids: %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatalf("collect ids: %v", err)
	}
	return ids
}

// rejectPublisher rejects one message ID and counts deliveries of the others.
type rejectPublisher struct {
	mu     sync.Mutex
	reject int64
	counts map[int64]int
}

func (p *rejectPublisher) Publish(_ context.Context, m Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m.ID == p.reject {
		return errors.New("rejected")
	}
	p.counts[m.ID]++
	return nil
}

// A message the publisher keeps rejecting must neither block the messages
// behind it nor make the ones before it get re-published on every batch.
func TestRelayRejectedMessageDoesNotBlockOrDuplicate(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	ctx := context.Background()
	ids := enqueueN(t, pool, 3)

	pub := &rejectPublisher{reject: ids[1], counts: map[int64]int{}}
	relay := NewRelay(pool, pub, slog.New(slog.DiscardHandler), 10, time.Second)
	for range 5 {
		_, _ = relay.ProcessBatch(ctx)
	}

	if pub.counts[ids[0]] != 1 || pub.counts[ids[2]] != 1 {
		t.Errorf("deliveries before/after the rejected message = %d/%d, want 1/1", pub.counts[ids[0]], pub.counts[ids[2]])
	}
	var (
		marked    int
		attempts  int32
		lastError *string
		backedOff bool
	)
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_messages WHERE published_at IS NOT NULL").Scan(&marked); err != nil {
		t.Fatal(err)
	}
	if marked != 2 {
		t.Errorf("%d messages marked published, want 2", marked)
	}
	if err := pool.QueryRow(ctx, "SELECT attempts, last_error, next_attempt_at > now() FROM outbox_messages WHERE id = $1", ids[1]).
		Scan(&attempts, &lastError, &backedOff); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastError == nil || *lastError != "rejected" {
		t.Errorf("rejected message attempts=%d last_error=%v, want 1 and \"rejected\" (retried only after back-off)", attempts, lastError)
	}
	if !backedOff {
		t.Error("rejected message is due again immediately, want it to back off")
	}
}

type countPublisher struct {
	mu sync.Mutex
	n  int
}

func (c *countPublisher) Publish(context.Context, Message) error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return nil
}

// A tick keeps draining while batches come back full, so throughput is not
// capped at one batch per interval.
func TestRelayDrainsBacklogWithinOneTick(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_messages (aggregate_id, event_type, payload)
		SELECT gen_random_uuid(), 'e', '{}'::jsonb FROM generate_series(1, 1000)`); err != nil {
		t.Fatal(err)
	}
	pub := &countPublisher{}
	// One batch per tick would manage at most 200 of the 1000 before the
	// deadline; draining publishes them all on the first tick.
	relay := NewRelay(pool, pub, slog.New(slog.DiscardHandler), 100, time.Second)
	runCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	go func() { _ = relay.Run(runCtx) }()

	for runCtx.Err() == nil {
		var left int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_messages WHERE published_at IS NULL").Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if pub.n != 1000 {
		t.Fatalf("published %d of 1000 within 2.5s at batch=100 interval=1s, want all", pub.n)
	}
}

// The retention purge must be served by an index rather than a full scan, and
// must remove a backlog larger than one delete batch.
func TestPurgePublishedUsesIndexAndBatches(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	ctx := context.Background()
	const old = purgeBatchSize*2 + 10
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_messages (aggregate_id, event_type, payload, published_at)
		SELECT gen_random_uuid(), 'e', '{}'::jsonb, now() - interval '30 days' FROM generate_series(1, $1::int)`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_messages (aggregate_id, event_type, payload, published_at)
		SELECT gen_random_uuid(), 'e', '{}'::jsonb, now() FROM generate_series(1, 100)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE outbox_messages"); err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SET enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, `EXPLAIN SELECT id FROM outbox_messages
		WHERE published_at IS NOT NULL AND published_at < now() - interval '7 days' LIMIT 5000`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "outbox_published_at_idx") {
		t.Errorf("purge predicate does not use outbox_published_at_idx:\n%s", joined)
	}

	relay := NewRelay(pool, &capturePublisher{}, slog.New(slog.DiscardHandler), 10, time.Second)
	removed, err := relay.PurgePublished(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if removed != old {
		t.Errorf("purged %d, want %d", removed, old)
	}
}
