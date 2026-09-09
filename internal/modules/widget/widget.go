// Package widget is the example vertical slice. A resource lives in one
// package: its SQL (queries.sql), persistence (store.go), business logic
// (service.go), and HTTP handlers (handler.go). The day-2 generator stamps new
// resources in this shape (see cmd/forge).
package widget

import "errors"

// Input is the validated, transport-independent data needed to create or
// replace a widget.
type Input struct {
	Name        string
	Description string
	Status      string
}

var (
	ErrNotFound  = errors.New("widget not found")
	ErrForbidden = errors.New("forbidden")
)
