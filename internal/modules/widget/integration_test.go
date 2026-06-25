//go:build integration

package widget

import (
	"context"
	"errors"
	"testing"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	"github.com/y0f/go-api-scaffolding/internal/testutil"
)

func TestRepositoryCRUD(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	svc := NewService(NewRepository(pool), nil)
	admin := auth.Principal{Subject: "tester", Roles: []string{"admin"}}
	ctx := context.Background()

	created, err := svc.Create(ctx, admin, Input{Name: "alpha", Description: "first", Status: "active"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "alpha" {
		t.Errorf("name = %q, want alpha", got.Name)
	}

	items, total, err := svc.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Errorf("list returned total=%d len=%d, want 1/1", total, len(items))
	}

	if err := svc.Delete(ctx, admin, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}
