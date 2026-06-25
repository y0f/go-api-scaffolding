# 2. Vertical-slice modules over horizontal layers

Date: 2026-06-25

## Status

Accepted

## Context

A service grows by adding features. Two common layouts are horizontal (global
`handlers`, `services`, `repositories` packages) and vertical (one package per
feature). Horizontal layers scatter a single feature across the tree and make a
"add a feature" generator awkward to write safely. Full hexagonal or DDD layering
adds ceremony that the median service does not need.

## Decision

Each resource is a package under `internal/modules` containing its SQL, store,
service, and handler. Cross-cutting infrastructure (database, http server,
observability, auth) lives under `internal/platform` and `internal/server`
behind interfaces. We keep light seams for swappable concerns and avoid
interfaces that exist only to satisfy a pattern.

## Consequences

Adding a feature means writing one directory, which is exactly what `forge add
resource` does. Blast radius stays small. The trade-off is some duplication
between slices, which we accept over premature shared abstractions.
