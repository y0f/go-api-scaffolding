//go:build integration

package widget

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	"github.com/y0f/go-api-scaffolding/internal/testutil"
)

type outboxRow struct {
	EventType string
	Payload   []byte
}

func outboxEvents(t *testing.T, pool *pgxpool.Pool, aggregateID uuid.UUID) []outboxRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT event_type, payload FROM outbox_messages WHERE aggregate_id = $1 ORDER BY id", aggregateID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.EventType, &r.Payload); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestEveryWriteEmitsAnOutboxEvent(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	svc := NewService(NewRepository(pool))
	admin := auth.Principal{Subject: "tester", Roles: []string{"admin"}}
	ctx := context.Background()

	created, err := svc.Create(ctx, admin, Input{Name: "alpha", Status: "active"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(ctx, admin, created.ID, Input{Name: "beta", Status: "archived"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := svc.Delete(ctx, admin, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	events := outboxEvents(t, pool, created.ID)
	want := []string{"widget.created", "widget.updated", "widget.deleted"}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, ev := range events {
		if ev.EventType != want[i] {
			t.Errorf("event %d = %s, want %s", i, ev.EventType, want[i])
		}
	}

	// updated carries the new API shape; deleted carries only the identity.
	var updated struct {
		ID     uuid.UUID `json:"id"`
		Name   string    `json:"name"`
		Status string    `json:"status"`
	}
	if err := json.Unmarshal(events[1].Payload, &updated); err != nil {
		t.Fatalf("decode updated payload: %v", err)
	}
	if updated.ID != created.ID || updated.Name != "beta" || updated.Status != "archived" {
		t.Errorf("updated payload = %+v, want id/beta/archived", updated)
	}
	var deleted map[string]any
	if err := json.Unmarshal(events[2].Payload, &deleted); err != nil {
		t.Fatalf("decode deleted payload: %v", err)
	}
	if deleted["id"] != created.ID.String() || len(deleted) != 1 {
		t.Errorf("deleted payload = %v, want only {\"id\": %s}", deleted, created.ID)
	}
}

// A write that fails must not leave an event behind: the event and the row
// share one transaction.
func TestMissingRowEmitsNoEvent(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	svc := NewService(NewRepository(pool))
	admin := auth.Principal{Subject: "tester", Roles: []string{"admin"}}
	ctx := context.Background()
	missing := uuid.New()

	if _, err := svc.Update(ctx, admin, missing, Input{Name: "x", Status: "active"}); err == nil {
		t.Fatal("update of a missing widget should fail")
	}
	if err := svc.Delete(ctx, admin, missing); err == nil {
		t.Fatal("delete of a missing widget should fail")
	}
	if events := outboxEvents(t, pool, missing); len(events) != 0 {
		t.Errorf("got %d events for a missing widget, want 0", len(events))
	}
}
