# Choosing a Go API starter

This scaffold fits HTTP JSON APIs backed by PostgreSQL, with explicit
authorization and an OpenAPI contract. Modules give parallel workers separate
areas to edit; one coordinator owns the shared contract and wiring. The
generated project owns its Go code.
Keep the reference resource while learning; add optional operational components
when the service needs them.

| Project | Useful when |
| --- | --- |
| [Go Blueprint](https://github.com/Melkeydev/go-blueprint) | You want several framework and database choices |
| [go-clean-template](https://github.com/evrone/go-clean-template) | You want HTTP, gRPC, and messaging examples |
| [go-service-template-rest](https://github.com/Dankosik/go-service-template-rest) | You want another OpenAPI/sqlc starter with agent guides |
| This scaffold | You want one Go/Postgres path, removable components, and an explicit parallel integration workflow |

These are scope differences, not quality rankings. Check current documentation
and run a representative feature task before choosing.

[Agent evaluations](https://github.com/y0f/gophling/tree/main/evals)
apply to the recorded scaffold revision,
environment, client, and model. They do not establish a universal ranking.
