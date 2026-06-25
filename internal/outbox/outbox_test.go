//go:build integration

package outbox

import (
	"context"
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
