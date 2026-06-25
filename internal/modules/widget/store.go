package widget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

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
	Create(ctx context.Context, in Input, claim *IdempotencyClaim) (db.Widget, error)
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

// errReserved is used only to trigger a rollback when the idempotency key was
// already claimed by a concurrent transaction.
var errReserved = errors.New("idempotency key reserved by another transaction")

// Create persists the widget and its creation event in one transaction. When a
// claim is present, the idempotency key is inserted in the same transaction; if
// a concurrent request already holds the key, the whole create is rolled back
// and ErrIdempotencyReserved is returned so the caller replays the stored
// response instead of creating a duplicate.
func (r *pgRepository) Create(ctx context.Context, in Input, claim *IdempotencyClaim) (db.Widget, error) {
	var created db.Widget
	err := database.WithinTx(ctx, r.pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		var txErr error
		created, txErr = queries.CreateWidget(ctx, in.Name, in.Description, in.Status)
		if txErr != nil {
			return txErr
		}
		event, txErr := json.Marshal(created)
		if txErr != nil {
			return txErr
		}
		if txErr = outbox.Enqueue(ctx, tx, created.ID, "widget.created", event); txErr != nil {
			return txErr
		}
		if claim == nil {
			return nil
		}
		body, txErr := json.Marshal(toAPIWidget(created))
		if txErr != nil {
			return txErr
		}
		rows, txErr := queries.PutIdempotencyKey(ctx, db.PutIdempotencyKeyParams{
			Key:            claim.Key,
			RequestHash:    claim.Hash,
			ResponseStatus: http.StatusCreated,
			ResponseBody:   body,
			ExpiresAt:      time.Now().Add(claim.TTL),
		})
		if txErr != nil {
			return txErr
		}
		if rows == 0 {
			return errReserved
		}
		return nil
	})
	switch {
	case errors.Is(err, errReserved):
		return db.Widget{}, ErrIdempotencyReserved
	case err != nil:
		return db.Widget{}, fmt.Errorf("create widget: %w", err)
	}
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
