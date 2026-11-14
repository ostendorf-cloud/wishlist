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

There is no secret-shaped variable yet. OIDC, lists and wishes, images, share tokens and
everything else `AGENTS.md`'s "Scope of this milestone" lists stays out of scope; only the
listen address and the log level are read from the environment so far.

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

Apache-2.0. See [LICENSE](LICENSE).
