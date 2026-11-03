package widget

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	db "github.com/y0f/go-api-scaffolding/internal/gen/db"
)

func (f *fakeRepo) CreateIdempotent(_ context.Context, in Input, _ IdempotencyClaim) (db.Widget, error) {
	f.createCalled = true
	return db.Widget{ID: uuid.New(), Name: in.Name, Status: in.Status}, nil
}

func TestCreateIdempotentRequiresWritePermission(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	_, err := svc.CreateIdempotent(context.Background(), auth.Principal{}, Input{Name: "x"}, IdempotencyClaim{Key: "k"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
	if repo.createCalled {
		t.Error("repository CreateIdempotent should not run when forbidden")
	}
}

func TestIdempotencyRowKeyIsUnambiguous(t *testing.T) {
	if idempotencyRowKey("a", "b:c") == idempotencyRowKey("a:b", "c") {
		t.Error("subjects containing ':' share row keys with other subjects")
	}
	if idempotencyRowKey("a%3Ab", "c") == idempotencyRowKey("a:b", "c") {
		t.Error("an escaped subject aliases a raw one")
	}
	// Rows stored before the escaping stay readable for ordinary subjects.
	if got := idempotencyRowKey("alice", "k:1"); got != "alice:k:1" {
		t.Errorf("row key = %q, want alice:k:1", got)
	}
}
