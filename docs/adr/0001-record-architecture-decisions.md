# 1. Record architecture decisions

Date: 2026-06-25

## Status

Accepted

## Context

Design choices in a scaffold outlive the people who made them. Without a record,
contributors relitigate settled questions and cannot tell which constraints are
deliberate.

## Decision

We keep Architecture Decision Records under `docs/adr`, one file per decision,
numbered sequentially. A decision is never deleted; it is superseded by a later
record. The format is Michael Nygard's: context, decision, consequences.

## Consequences

Decisions are discoverable and dated. Reversing one means writing a new ADR that
supersedes it, which keeps the history honest.
