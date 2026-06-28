//go:build integration

package outbox

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

	// The first publish fails, so the batch transaction rolls back and the
	// message stays unpublished; the next batch must redeliver it at-least-once.
	pub := &flakyPublisher{failUntil: 1}
	relay := NewRelay(pool, pub, slog.Default(), 10, time.Second)

	if _, err := relay.ProcessBatch(ctx); err == nil {
		t.Fatal("expected the first batch to fail")
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
