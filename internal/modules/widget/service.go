package widget

import (
	"context"

	"github.com/google/uuid"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	db "github.com/y0f/go-api-scaffolding/internal/gen/db"
)

// PermissionWrite is required to create, replace, or delete a widget.
const PermissionWrite = "widgets:write"

// Service holds the business rules for widgets, including authorization. It
// depends on the Repository interface, so it is unit-testable without a
// database.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, actor auth.Principal, in Input, claim *IdempotencyClaim) (db.Widget, error) {
	if !actor.HasPermission(PermissionWrite) {
		return db.Widget{}, ErrForbidden
	}
	return s.repo.Create(ctx, in, claim)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (db.Widget, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, limit, offset int32) ([]db.Widget, int64, error) {
	return s.repo.List(ctx, limit, offset)
}

func (s *Service) Update(ctx context.Context, actor auth.Principal, id uuid.UUID, in Input) (db.Widget, error) {
	if !actor.HasPermission(PermissionWrite) {
		return db.Widget{}, ErrForbidden
	}
	return s.repo.Update(ctx, id, in)
}

func (s *Service) Delete(ctx context.Context, actor auth.Principal, id uuid.UUID) error {
	if !actor.HasPermission(PermissionWrite) {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, id)
}
