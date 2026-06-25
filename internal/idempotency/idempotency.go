// Package idempotency stores and replays responses to unsafe requests keyed by
// a client-supplied Idempotency-Key, so retries do not duplicate side effects.
// The key is written in the same transaction as the state change it guards (see
// the widget module), which makes concurrent retries safe as well as sequential
// ones. This package owns lookup, hashing, and the time-to-live.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/y0f/go-api-scaffolding/internal/gen/db"
)

// ErrConflict is returned when a key is reused with a different request body,
// which the caller should surface as HTTP 409.
var ErrConflict = errors.New("idempotency key reused with a different request")

// Record is a previously stored response.
type Record struct {
	Status int
	Body   []byte
}

type Store struct {
	pool *pgxpool.Pool
	ttl  time.Duration
}

func NewStore(pool *pgxpool.Pool, ttl time.Duration) *Store {
	return &Store{pool: pool, ttl: ttl}
}

// TTL is how long a stored key remains replayable.
func (s *Store) TTL() time.Duration { return s.ttl }

// Hash derives the request fingerprint compared across retries of a key. Each
// part is length-prefixed so that part boundaries cannot alias.
func Hash(parts ...[]byte) string {
	h := sha256.New()
	var length [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(p)))
		h.Write(length[:])
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Lookup returns the stored response for key. If the key exists but was stored
// for a different request fingerprint, it returns ErrConflict.
func (s *Store) Lookup(ctx context.Context, key, requestHash string) (*Record, error) {
	row, err := db.New(s.pool).GetIdempotencyKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup idempotency key: %w", err)
	}
	if row.RequestHash != requestHash {
		return nil, ErrConflict
	}
	return &Record{Status: int(row.ResponseStatus), Body: row.ResponseBody}, nil
}
