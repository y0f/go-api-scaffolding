# 4. Build on net/http and chi, not a framework

Date: 2026-06-25

## Status

Accepted

## Context

Some web frameworks introduce their own context type and handler signature, which
couples every handler to that dependency. Since Go 1.22 the standard library router
handles method and path matching, and chi adds grouping and middleware while
staying `http.Handler`-compatible.

## Decision

Handlers are standard `http.Handler` functions. Routing uses chi, kept behind the
generated server interface. There is no framework-specific context. The OpenAPI
validator and all middleware are plain `func(http.Handler) http.Handler`.

## Consequences

The code stays portable and any net/http middleware works without adapters. The
trade-off is that some conveniences a batteries-included framework provides are
written explicitly here, which we consider worthwhile for a foundation others
build on.
