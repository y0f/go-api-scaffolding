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
	if err := enqueueWidgetEvent(ctx, tx, "widget.created", created); err != nil {
		return db.Widget{}, err
	}
	//forge:end outbox
	return created, nil
}

//forge:begin outbox

// enqueueWidgetEvent records a lifecycle event in the same transaction as the
// change. The event carries the API shape of the widget, so subscribers see
// what API clients see.
func enqueueWidgetEvent(ctx context.Context, tx pgx.Tx, eventType string, w db.Widget) error {
	payload, err := json.Marshal(toAPIWidget(w))
	if err != nil {
		return err
	}
	return outbox.Enqueue(ctx, tx, w.ID, eventType, payload)
}

//forge:end outbox

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

// Update replaces the widget, and records its update event, in one transaction.
func (r *pgRepository) Update(ctx context.Context, id uuid.UUID, in Input) (db.Widget, error) {
	var updated db.Widget
	err := database.WithinTx(ctx, r.pool, func(tx pgx.Tx) error {
		var txErr error
		updated, txErr = db.New(tx).UpdateWidget(ctx, id, in.Name, in.Description, in.Status)
		if txErr != nil {
			return txErr
		}
		//forge:begin outbox
		if txErr := enqueueWidgetEvent(ctx, tx, "widget.updated", updated); txErr != nil {
			return txErr
		}
		//forge:end outbox
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Widget{}, ErrNotFound
	}
	if err != nil {
		return db.Widget{}, fmt.Errorf("update widget: %w", err)
	}
	return updated, nil
}

// Delete removes the widget, and records its deletion event, in one
// transaction. A missing row rolls back so no event is left behind.
func (r *pgRepository) Delete(ctx context.Context, id uuid.UUID) error {
	err := database.WithinTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, txErr := db.New(tx).DeleteWidget(ctx, id)
		if txErr != nil {
			return txErr
		}
		if rows == 0 {
			return ErrNotFound
		}
		//forge:begin outbox
		// Only the identity: the row is gone, and a subscriber that needs the
		// last state has it from the preceding events.
		if txErr := outbox.Enqueue(ctx, tx, id, "widget.deleted", []byte(`{"id":"`+id.String()+`"}`)); txErr != nil {
			return txErr
		}
		//forge:end outbox
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete widget: %w", err)
	}
	return nil
}
