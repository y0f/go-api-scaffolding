// Package widget is the example vertical slice. A resource lives in one
// package: its SQL (queries.sql), persistence (store.go), business logic
// (service.go), and HTTP handlers (handler.go). The day-2 generator stamps new
// resources in this shape (see cmd/forge).
package widget

import (
	"errors"
	"time"
)

// Input is the validated, transport-independent data needed to create or
// replace a widget.
type Input struct {
	Name        string
	Description string
	Status      string
}

// IdempotencyClaim ties a create to a client-supplied Idempotency-Key. When set,
// the key is inserted in the same transaction as the widget, so a concurrent
// retry that loses the race is rolled back rather than creating a duplicate.
type IdempotencyClaim struct {
	Key  string
	Hash string
	TTL  time.Duration
}

var (
	ErrNotFound  = errors.New("widget not found")
	ErrForbidden = errors.New("forbidden")
	// ErrIdempotencyReserved means another request already claimed the key; the
	// caller should replay the stored response.
	ErrIdempotencyReserved = errors.New("idempotency key already reserved")
)
