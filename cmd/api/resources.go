package main

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	api "github.com/y0f/gophling/internal/gen/api"
	"github.com/y0f/gophling/internal/idempotency"
	"github.com/y0f/gophling/internal/modules/widget"
	// forge:resource-imports
)

// applicationAPI explicitly delegates the contract to its resource handlers.
// Keep business rules in each module; this file only composes HTTP operations.
type applicationAPI struct {
	//forge:begin example
	widget *widget.Handler
	//forge:end example
	// forge:resource-fields
}

var _ api.ServerInterface = (*applicationAPI)(nil)

func newApplicationAPI(pool *pgxpool.Pool,
	//forge:begin idempotency
	idemStore *idempotency.Store,
	//forge:end idempotency
) *applicationAPI {
	// Keep the factory usable before the first resource is added.
	_ = pool
	//forge:begin idempotency
	_ = idemStore
	//forge:end idempotency
	app := &applicationAPI{}
	//forge:begin example
	app.widget = widget.NewHandler(widget.NewService(widget.NewRepository(pool)))
	//forge:begin idempotency
	app.widget.EnableIdempotency(idemStore)
	//forge:end idempotency
	//forge:end example
	// forge:resource-wiring
	return app
}

//forge:begin example
func (a *applicationAPI) ListWidgets(w http.ResponseWriter, r *http.Request, params api.ListWidgetsParams) {
	a.widget.ListWidgets(w, r, params)
}

func (a *applicationAPI) CreateWidget(w http.ResponseWriter, r *http.Request) {
	a.widget.CreateWidget(w, r)
}

func (a *applicationAPI) GetWidget(w http.ResponseWriter, r *http.Request, id api.WidgetID) {
	a.widget.GetWidget(w, r, id)
}

func (a *applicationAPI) UpdateWidget(w http.ResponseWriter, r *http.Request, id api.WidgetID) {
	a.widget.UpdateWidget(w, r, id)
}

func (a *applicationAPI) DeleteWidget(w http.ResponseWriter, r *http.Request, id api.WidgetID) {
	a.widget.DeleteWidget(w, r, id)
}

//forge:end example
