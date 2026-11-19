# wishlist

The rewrite of `old_wishlist` as a Go API behind a Vue SPA on govue.

## Problem

- `old_wishlist` renders HTML from Go templates; every other `ostendorf-cloud` application is a
  Go API behind a Vue SPA on govue, so this one no longer matches how the organisation builds,
  deploys and operates its services.
- The owner of a list must never learn what a guest reserved. `old_wishlist` kept that promise by
  never putting reservation data into any page the owner's browser could load, a property of its
  queries and its template data, proven by tests on both. A rewrite that talks JSON instead of
  rendering HTML removes the mechanism that test relied on without removing the requirement.
- Adding a wish from a shop link costs the owner a title, a picture and a price typed or copied by
  hand; `old_wishlist`'s own form never pulled any of the three off the page automatically.

## Goals

- The feature set of `old_wishlist` works unchanged in meaning: owner sign in, lists, wishes,
  images in an object store, share tokens, guests reserving and releasing, comments, two
  languages, link previews when adding a wish.
- The owner signs in with an own account first: an email address, a password and TOTP, through
  govue's `account` package over this service's own tables, with a cookie session and a CSRF
  token. OIDC comes later as a second way in to the same owner row. Passkeys are a later
  second factor next to TOTP, so no account assumes exactly one.
- A reservation stays blind to the owner, and that guarantee has its own test in this repository,
  not only a review, the same way `old_wishlist`'s `blind_test.go` and `blindness_test.go` did.
- A guest reaches a shared list and can reserve, release and comment without creating an account or
  logging in.
- Importing a wish from a product link yields a usable title, picture and price for a large shop,
  which `old_wishlist`'s own form never did.
- The service matches this repository's existing scaffold: Go API, Vue SPA, health, readiness and
  metrics on govue's admin listener, one Helm chart, one CI.

## Non goals

- A new database or object store. PostgreSQL through pgx stays as it is. The service runs no
  identity provider of its own: the owner accounts are local credentials, and OIDC, when it
  comes, is a second way in to the same owner row.
- Any capability `old_wishlist` does not have, including any view that gives the owner reservation
  data for support or debugging.
- The exact JSON shape of every endpoint; that is implementation, not this concept.
- A new guest capability such as an account, a notification, or a wishlist the guest keeps.

## Approach

- One Go process serves two HTTP surfaces on the public listener, as `old_wishlist` served two
  route groups: an owner API behind govue's account, session and CSRF packages (own accounts,
  OIDC later as a second way in), and a guest API
  reached only by share token, never by login. Health, readiness and metrics stay on govue's
  admin listener, as the scaffold already has them.
- The store layer keeps `old_wishlist`'s split: the guest-only queries, including reservations,
  live in one file; every other store file is checked, the same way, for a SQL string naming the
  reservations table.
- The handler layer keeps `old_wishlist`'s second check in a new shape: no owner API response type
  may reach a type that carries a reservation, checked by a test over the Go types the JSON
  encoder serialises, in place of the old reflection test over Go template page data. See
  ADR 0001.
- A guest's own identity is a signed, cookie-carried key minted on first visit to a share link, the
  same shape `old_wishlist`'s guest key had, carried through govue's session codec instead of
  `old_wishlist`'s own `internal/session`; it is what lets a guest release their own reservation or
  delete their own comment on a later visit.
- Link import stays an owner-only action, as `old_wishlist`'s `handlePreview` already was: the
  fetch keeps `old_wishlist`'s SSRF guard unchanged in meaning — scheme check, redirect
  re-validation, DNS-resolved address class check against private, loopback, link-local, CGNAT and
  documentation ranges, and a bounded size, time and content type.
- The API returns data and stable keys, not rendered text. The Vue SPA carries the locale catalog
  and the fallback chain `old_wishlist` ran server side: explicit choice, then cookie, then
  Accept-Language, then a default.
- Images, share tokens, reserving, releasing and comments carry over in meaning, as API endpoints
  in place of form posts.

## Alternatives

- Render HTML server side and add a JSON API alongside it for interactivity: dropped, it keeps two
  rendering paths to hold blind instead of one, and it does not reach parity with the other
  `ostendorf-cloud` applications, which is the reason for this rewrite.
- A privileged endpoint that lets the owner's dashboard read a reservation for support purposes:
  dropped, it reopens exactly the leak the blindness property forbids; `old_wishlist` never had one
  either.
- An account for every guest, by magic link or password: dropped, `old_wishlist` works without one,
  and an account adds a login surface to a feature, reserving a gift, that works better anonymous.

- The owner's account is the owner row. The session keys on the row id, never on an email
  address or a provider subject, so a second way in (OIDC, passkeys) maps to the same id.
  Sessions live in the memory of one process until a shared session store exists, which is why
  the chart allows one replica.

## Open questions

- Whether the guest's identity is the cookie-bound key alone, or a signed token the SPA also
  carries explicitly, since the API has no page render left to mint a cookie during — decide
  before the guest API is implemented.
- Whether an imported price is written to the wish only once the owner saves the form, matching
  `old_wishlist`, or earlier — decide in the link-import implementation issue.
- What the SPA shows when a shop blocks or times out the import fetch: empty fields for the owner
  to fill by hand, matching `old_wishlist`, or an explicit error — decide in the same issue.
- The locale negotiation contract between the SPA and the API: header, query parameter or cookie,
  and which side owns the decision — decide before the i18n implementation issue.

## Related decisions

- [ADR 0001](../adr/0001-blind-reservations.md): a reservation is blind by construction, not by an
  authorization check.

## License

Apache-2.0, like the other `ostendorf-cloud` applications.
