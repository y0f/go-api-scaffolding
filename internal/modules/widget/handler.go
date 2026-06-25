package widget

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	api "github.com/y0f/go-api-scaffolding/internal/gen/api"
	db "github.com/y0f/go-api-scaffolding/internal/gen/db"
	"github.com/y0f/go-api-scaffolding/internal/idempotency"
	"github.com/y0f/go-api-scaffolding/internal/platform/problem"
)

// Handler adapts HTTP to the widget service. It implements the generated
// api.ServerInterface for the widget operations.
type Handler struct {
	service     *Service
	idempotency *idempotency.Store
}

func NewHandler(service *Service, idem *idempotency.Store) *Handler {
	return &Handler{service: service, idempotency: idem}
}

func (h *Handler) ListWidgets(w http.ResponseWriter, r *http.Request, params api.ListWidgetsParams) {
	limit, offset := int32(20), int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Offset != nil {
		offset = *params.Offset
	}
	items, total, err := h.service.List(r.Context(), limit, offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := api.WidgetList{
		Items:      make([]api.Widget, 0, len(items)),
		Pagination: api.Pagination{Limit: limit, Offset: offset, Total: total},
	}
	for _, item := range items {
		out.Items = append(out.Items, toAPIWidget(item))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) CreateWidget(w http.ResponseWriter, r *http.Request, params api.CreateWidgetParams) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		problem.Status(w, r, http.StatusBadRequest, "cannot read request body")
		return
	}
	var input api.WidgetInput
	if err := json.Unmarshal(body, &input); err != nil {
		problem.Status(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	actor, _ := auth.PrincipalFrom(r.Context())

	var claim *IdempotencyClaim
	if params.IdempotencyKey != nil && *params.IdempotencyKey != "" {
		key := *params.IdempotencyKey
		hash := idempotency.Hash([]byte(r.URL.Path), body)
		if h.maybeReplay(w, r, key, hash) {
			return
		}
		claim = &IdempotencyClaim{Key: key, Hash: hash, TTL: h.idempotency.TTL()}
	}

	created, err := h.service.Create(r.Context(), actor, inputFromAPI(input), claim)
	if errors.Is(err, ErrIdempotencyReserved) {
		// A concurrent request claimed the key first; replay its stored response.
		if h.maybeReplay(w, r, claim.Key, claim.Hash) {
			return
		}
		h.writeError(w, r, err)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	payload, err := json.Marshal(toAPIWidget(created))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/widgets/"+created.ID.String())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(payload)
}

// maybeReplay writes a stored idempotent response when one exists for key and
// hash. It returns true when it has written a response (a replay or a 409).
func (h *Handler) maybeReplay(w http.ResponseWriter, r *http.Request, key, hash string) bool {
	record, err := h.idempotency.Lookup(r.Context(), key, hash)
	switch {
	case errors.Is(err, idempotency.ErrConflict):
		problem.Status(w, r, http.StatusConflict, "idempotency key already used for a different request")
		return true
	case err != nil:
		h.writeError(w, r, err)
		return true
	case record != nil:
		replay(w, record)
		return true
	default:
		return false
	}
}

func (h *Handler) GetWidget(w http.ResponseWriter, r *http.Request, id api.WidgetID) {
	found, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIWidget(found))
}

func (h *Handler) UpdateWidget(w http.ResponseWriter, r *http.Request, id api.WidgetID) {
	var input api.WidgetInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		problem.Status(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	actor, _ := auth.PrincipalFrom(r.Context())
	updated, err := h.service.Update(r.Context(), actor, id, inputFromAPI(input))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIWidget(updated))
}

func (h *Handler) DeleteWidget(w http.ResponseWriter, r *http.Request, id api.WidgetID) {
	actor, _ := auth.PrincipalFrom(r.Context())
	if err := h.service.Delete(r.Context(), actor, id); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		problem.Status(w, r, http.StatusNotFound, "widget not found")
	case errors.Is(err, ErrForbidden):
		problem.Status(w, r, http.StatusForbidden, "you do not have permission to perform this action")
	default:
		slog.ErrorContext(r.Context(), "widget handler error", slog.Any("error", err))
		problem.Status(w, r, http.StatusInternalServerError, "internal server error")
	}
}

func toAPIWidget(w db.Widget) api.Widget {
	return api.Widget{
		Id:          w.ID,
		Name:        w.Name,
		Description: w.Description,
		Status:      api.WidgetStatus(w.Status),
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
	}
}

func inputFromAPI(in api.WidgetInput) Input {
	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	status := string(api.Active)
	if in.Status != nil {
		status = string(*in.Status)
	}
	return Input{Name: in.Name, Description: description, Status: status}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func replay(w http.ResponseWriter, record *idempotency.Record) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Idempotency-Replayed", "true")
	if id := widgetIDFromBody(record.Body); id != "" {
		w.Header().Set("Location", "/v1/widgets/"+id)
	}
	w.WriteHeader(record.Status)
	// body is a previously stored JSON response, served as application/json.
	_, _ = w.Write(record.Body) //#nosec G705
}

func widgetIDFromBody(body []byte) string {
	var probe struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.ID
}
