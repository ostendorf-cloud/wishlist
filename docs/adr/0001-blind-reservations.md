# Blind reservations by construction

## Status

Accepted. Supersedes ADR 65 of the predecessor repository this one was copied from, not available
in this organisation.

## Context

- `old_wishlist`'s schema comment (`internal/store/migrations/0001_init.sql`) states the rule this
  record is about: "Two halves that never meet in a query: owners, lists and wishes, which the
  owner of a list reads and writes, and reservations, which only the guest side reads. The owner
  never learns what was taken (ADR 65), and that is a property of the queries in owner.go, kept
  honest by blind_test.go."
- `old_wishlist` enforces that property at the store layer: only `guest.go` and `dialog.go` in
  `internal/store` query the `reservations` table; every other file in that package is checked by
  `TestOnlyTheGuestFileQueriesReservations`, which parses each file's syntax tree and fails on any
  SQL string literal naming that table.
- `old_wishlist` enforces the same property at the handler layer in `cmd/wishlist`:
  `TestOwnerHandlersCallNoGuestStoreMethod` fails if an owner handler calls a store method marked
  guest-only, and `TestOwnerPageDataCannotCarryAReservation` fails if any type an owner page
  renders can reach a `Reservation`, `GuestWish` or `GuestList` value through reflection.
- Both the schema comment and the two blindness tests name "ADR 65" as the record of this
  decision, in a predecessor repository that is not available in this organisation.
- This repository rewrites `old_wishlist` as a Go API behind a Vue SPA: the owner and the guest
  now reach the same process over HTTP endpoints and JSON responses, not rendered pages, so the
  mechanism that kept the old tests meaningful (Go template page data) no longer exists in the
  same shape.

## Decision

A reservation stays blind to the owner by construction, enforced at the query layer and at the
response layer, each kept honest by its own test, the same way `old_wishlist` enforced it; an
authorization check alone is not the decision this record makes.

## Consequences

- The guarantee survives a handler or endpoint added later without review catching it, because a
  test fails rather than relying on someone noticing a reservation field in an owner response.
- The reasoning is on record in one place instead of being implicit in query layout and file
  naming, so a contributor who did not read `old_wishlist` can find out why an owner-facing store
  method must never read the `reservations` table.
- Every owner-facing response type and every store method has to be classified owner-only or
  guest-only up front, and a field that legitimately belongs to both sides, comments in
  `old_wishlist`, needs the same careful handling `dialog.go` gave it, or the enforcing test gives
  a false result.
- The enforcing test itself has to be rewritten for JSON response types and HTTP handlers in place
  of Go templates and page-data structs; until that test exists in this repository, the guarantee
  rests on review alone. See the open question on this in `docs/concepts/wishlist.md`.

## Alternatives

- An authorization check in the handler instead of a structural one: rejected by the decision
  this supersedes and by `old_wishlist`'s own comment that the property belongs to the queries,
  not to an authorization check; a check can be bypassed by a later code path, a structural
  separation does not drift the same way.
- No carried-over decision, left to review alone in this repository: rejected, blind reservations
  are the one property the whole application exists to keep.

## Related

- [Concept: wishlist](../concepts/wishlist.md)
