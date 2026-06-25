package widget

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	db "github.com/y0f/go-api-scaffolding/internal/gen/db"
)

type fakeRepo struct {
	createCalled bool
	deleteCalled bool
}

func (f *fakeRepo) Create(_ context.Context, in Input, _ *IdempotencyClaim) (db.Widget, error) {
	f.createCalled = true
	return db.Widget{ID: uuid.New(), Name: in.Name, Status: in.Status}, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (db.Widget, error) {
	return db.Widget{ID: id}, nil
}

func (f *fakeRepo) List(_ context.Context, _, _ int32) ([]db.Widget, int64, error) {
	return []db.Widget{{}}, 1, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, in Input) (db.Widget, error) {
	return db.Widget{ID: id, Name: in.Name}, nil
}

func (f *fakeRepo) Delete(_ context.Context, _ uuid.UUID) error {
	f.deleteCalled = true
	return nil
}

func TestCreateRequiresWritePermission(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, nil)
	_, err := svc.Create(context.Background(), auth.Principal{Roles: []string{"viewer"}}, Input{Name: "x"}, nil)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
	if repo.createCalled {
		t.Error("repository Create should not run when forbidden")
	}
}

func TestCreateAllowedForAdmin(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, nil)
	got, err := svc.Create(context.Background(), auth.Principal{Roles: []string{"admin"}}, Input{Name: "x", Status: "active"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Name != "x" {
		t.Errorf("name = %q, want x", got.Name)
	}
	if !repo.createCalled {
		t.Error("repository Create should run for admin")
	}
}

func TestDeleteRequiresPermission(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, nil)
	if err := svc.Delete(context.Background(), auth.Principal{}, uuid.New()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
	if repo.deleteCalled {
		t.Error("repository Delete should not run when forbidden")
	}
}
