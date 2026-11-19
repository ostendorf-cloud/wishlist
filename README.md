# wishlist

Shared wish lists with reservations the owner never sees.

## What it does

A Go service serves a Vue 3 single page application built on the organisation's own `govue`
framework. This is a rewrite of an earlier, server-rendered wishlist application; see
`AGENTS.md` for the feature set that rewrite carries forward and what this milestone leaves
out.

## Install

```sh
task setup
```

Installs the pinned toolchain (Go, Node, pnpm, and the lint and release tooling) through
`mise`.

## Use

```sh
task web:build   # builds the SPA into cmd/wishlist/dist
task build        # compiles the wishlist binary into bin/wishlist
bin/wishlist
```

The binary reads its configuration from the environment; see the Configuration table below.
`/healthz`, `/readyz` and `/metrics` answer on a second, administrative listener
(`ADMIN_ADDR`), never on the public one.

## Configuration

| Variable | Purpose | Default |
| --- | --- | --- |
| `ADDR` | Address the public HTTP server listens on | `:8080` |
| `ADMIN_ADDR` | Address govue's admin listener listens on: `/healthz`, `/readyz` and `/metrics` | `:8081` |
| `LOG_LEVEL` | `debug`, `info`, `warn` or `error` | `info` |
| `DB_HOST` | PostgreSQL host | required |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_NAME` | Database name | required |
| `DB_USER` | Database role | required |
| `DB_PASSWORD` or `DB_PASSWORD_FILE` | The role's password, directly or from a file. Never both | required |
| `DB_SSLMODE` | `disable`, `allow`, `prefer`, `require`, `verify-ca` or `verify-full` | `require` |
| `DB_MAX_CONNS` | Pool size per pod, 1 to 100 | `10` |
| `DB_CONNECT_TIMEOUT` | One attempt to open a connection, 1s to 1m | `5s` |
| `SITE_URL` | Public origin of the site; its bare host is sent to Umami as the hostname | `https://my-wishes.de` |
| `TRUSTED_PROXIES` | Comma separated addresses or CIDRs of the proxies in front of the service, whose `X-Forwarded-For` is believed. Behind a proxy without it no view is counted | unset |
| `UMAMI_URL` | Base URL of a Umami instance; page views are reported to it from the server | unset |
| `UMAMI_WEBSITE_ID` | Umami website id (a UUID) | unset |
| `UMAMI_TIMEOUT`, `UMAMI_QUEUE_SIZE` | Optional govue settings: post timeout and queue size | `5s`, `256` |

Every secret-shaped variable has a `_FILE` twin. The schema is applied at startup, and
`/readyz` stays unready until the database is reachable and the schema is applied.
`WISHLIST_TEST_DATABASE_URL` is read by the tests only, see `AGENTS.md`. OIDC and everything
else `AGENTS.md`'s "Scope of this milestone" lists stays out of scope.

## Page view counting

Counting is off unless `UMAMI_URL` and `UMAMI_WEBSITE_ID` are both set. Setting only one stops
the start.

- With counting on, the server reports one page view to Umami after it served the page.
  Counting needs no script in the browser, sets no cookie and stores nothing on the device.
- Reported: the page as the route pattern, never the path, an id, a share token or a query
  string, the origin of the referring site, the browser language, the user agent and the client
  address shortened to its first three bytes (IPv4) or six (IPv6).
- Not counted: a visitor who sends Do Not Track or Global Privacy Control, prefetch, the API,
  assets, health and metrics routes, and anything that is not a successful HTML page.
- A guest's reservation and share routes are never reported by id or token: only the pattern is.
- The privacy text states the operator's facts, which the code cannot show: Umami and the site
  run on the owner's own hardware in Germany, access logs are kept 30 days, Umami data 14
  months, the legal basis is Art. 6(1)(f) GDPR (accounts: Art. 6(1)(b)). Change them in
  `PrivacyPage.vue` and `legal.test.ts` together.
- The text lives in `web/src/legal/PrivacyPage.vue`, in German and English.

## Container

```sh
docker compose up --build
```

Builds the multi-stage `Dockerfile` (frontend, static Go binary, then a minimal non-root
runtime stage with no shell) and serves the site at `http://localhost:8080`.
`docker-compose.yaml` is for local development only; see `charts/wishlist/README.md` for a
cluster installation.

## Development

```sh
task setup
task check
```

`task check` runs every lint, build and test target one after another; `task --list` shows
them individually. `task web:build` has to run before `task build` or `task test:go` can
compile `cmd/wishlist`, since it embeds the built frontend; both already declare that
dependency, so running either one through Task is enough.

Releases are driven by [release-me](https://github.com/ostendorf-cloud/release-me):
Conventional Commits decide the version, and merging the release pull request it maintains
cuts the release. `CHANGELOG.md` is generated, never hand edited.

## License

[Apache-2.0](LICENSE).
