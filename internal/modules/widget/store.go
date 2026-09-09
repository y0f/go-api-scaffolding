package widget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/y0f/go-api-scaffolding/internal/gen/db"
	"github.com/y0f/go-api-scaffolding/internal/outbox"
	"github.com/y0f/go-api-scaffolding/internal/platform/database"
)

// Repository is the persistence seam. Swap the implementation to change the
// datastore without touching the service or handler.
type Repository interface {
	Create(ctx context.Context, in Input) (db.Widget, error)
	//forge:begin idempotency
	CreateIdempotent(ctx context.Context, in Input, claim IdempotencyClaim) (db.Widget, error)
	//forge:end idempotency
	Get(ctx context.Context, id uuid.UUID) (db.Widget, error)
	List(ctx context.Context, limit, offset int32) ([]db.Widget, int64, error)
	Update(ctx context.Context, id uuid.UUID, in Input) (db.Widget, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type pgRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

// Create persists the widget, and its creation event, in one transaction.
func (r *pgRepository) Create(ctx context.Context, in Input) (db.Widget, error) {
	var created db.Widget
	err := database.WithinTx(ctx, r.pool, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = r.insert(ctx, tx, in)
		return txErr
	})
	if err != nil {
		return db.Widget{}, fmt.Errorf("create widget: %w", err)
	}
	return created, nil
}

// insert writes the row within tx, so a caller can add more to the same
// transaction.
func (r *pgRepository) insert(ctx context.Context, tx pgx.Tx, in Input) (db.Widget, error) {
	created, err := db.New(tx).CreateWidget(ctx, in.Name, in.Description, in.Status)
	if err != nil {
		return db.Widget{}, err
	}
	//forge:begin outbox
	// The event carries the API shape of the widget, so subscribers see what
	// API clients see.
	event, err := json.Marshal(toAPIWidget(created))
	if err != nil {
		return db.Widget{}, err
	}
	if err := outbox.Enqueue(ctx, tx, created.ID, "widget.created", event); err != nil {
		return db.Widget{}, err
	}
	//forge:end outbox
	return created, nil
}

func (r *pgRepository) Get(ctx context.Context, id uuid.UUID) (db.Widget, error) {
	found, err := db.New(r.pool).GetWidget(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Widget{}, ErrNotFound
	}
	if err != nil {
		return db.Widget{}, fmt.Errorf("get widget: %w", err)
	}
	return found, nil
}

func (r *pgRepository) List(ctx context.Context, limit, offset int32) ([]db.Widget, int64, error) {
	queries := db.New(r.pool)
	items, err := queries.ListWidgets(ctx, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list widgets: %w", err)
	}
	total, err := queries.CountWidgets(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count widgets: %w", err)
	}
	return items, total, nil
}

func (r *pgRepository) Update(ctx context.Context, id uuid.UUID, in Input) (db.Widget, error) {
	updated, err := db.New(r.pool).UpdateWidget(ctx, id, in.Name, in.Description, in.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Widget{}, ErrNotFound
	}
	if err != nil {
		return db.Widget{}, fmt.Errorf("update widget: %w", err)
	}
	return updated, nil
}

func (r *pgRepository) Delete(ctx context.Context, id uuid.UUID) error {
	rows, err := db.New(r.pool).DeleteWidget(ctx, id)
	if err != nil {
		return fmt.Errorf("delete widget: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
