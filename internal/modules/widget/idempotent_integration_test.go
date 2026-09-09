//go:build integration

package widget

import (
	"context"
	"testing"
	"time"

	"github.com/y0f/go-api-scaffolding/internal/testutil"
)

// TestCreateReclaimsExpiredIdempotencyKey is a regression test: a key reused
// after its TTL expired but before the reaper purges it must reclaim the stale
// row and create the widget, not return ErrIdempotencyReserved with a lost
// create.
func TestCreateReclaimsExpiredIdempotencyKey(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	expired := IdempotencyClaim{Key: "reused", Hash: "hash-a", TTL: -time.Minute}
	if _, err := repo.CreateIdempotent(ctx, Input{Name: "first", Status: "active"}, expired); err != nil {
		t.Fatalf("first create: %v", err)
	}

	fresh := IdempotencyClaim{Key: "reused", Hash: "hash-b", TTL: time.Hour}
	second, err := repo.CreateIdempotent(ctx, Input{Name: "second", Status: "active"}, fresh)
	if err != nil {
		t.Fatalf("reuse after expiry: %v", err)
	}
	if second.Name != "second" {
		t.Errorf("name = %q, want second", second.Name)
	}
}
