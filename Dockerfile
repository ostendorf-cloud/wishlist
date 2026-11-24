# syntax=docker/dockerfile:1@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183

# Frontend stage: builds the Vue SPA bundle straight into cmd/wishlist/dist, the
# same path "task web:build" writes to locally. The Go build stage below only
# ever copies that directory out, never runs Node itself.
#
# Digest for node:26.13.1-bookworm-slim is the multi-platform image index
# (linux/amd64, linux/arm64, linux/ppc64le), resolved against Docker Hub's
# API; re-resolve with
# "docker buildx imagetools inspect node:26.13.1-bookworm-slim" if this
# patch version ever moves. That command reports the index digest below,
# the same one a plain "docker pull" resolves against on either platform.
FROM node:26.13.1-bookworm-slim@sha256:0042e17d232a87a2cd485be1da7900fca4d10a4ad11b29e9285da1d2e54798c9 AS frontend

WORKDIR /src/web

# corepack no longer ships with Node: the Node.js TSC voted to stop bundling it
# by default starting with Node.js 25 (nodejs/corepack#722). npm still ships,
# so npm installs corepack first, then corepack installs the pinned pnpm
# version below, the exact one mise.toml's "pnpm" tool resolves to, so the
# lockfile this stage reads was written by the same pnpm a local "task setup"
# installs.
RUN npm install -g corepack@0.36.0 && corepack enable && corepack prepare pnpm@12.19.0 --activate

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./

RUN pnpm run build

# Digest for golang:1.27.4-bookworm is the multi-platform image index
# (linux/amd64, linux/arm64, linux/386, linux/arm/v7, linux/ppc64le),
# matching mise.toml's pinned Go patch exactly; resolved against Docker
# Hub's API. Re-resolve with
# "docker buildx imagetools inspect golang:1.27.4-bookworm" if this patch
# version ever moves; that command reports the index digest below.
FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=frontend /src/cmd/wishlist/dist cmd/wishlist/dist

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wishlist ./cmd/wishlist

# distroless/static-debian12 carries only a minimal root filesystem,
# ca-certificates and tzdata, no shell, no package manager. The ":nonroot" tag
# runs as uid/gid 65532, not root.
#
# Digest for gcr.io/distroless/static-debian12:nonroot is the multi-platform
# image index (linux/amd64, linux/arm64, linux/arm/v7, linux/s390x,
# linux/ppc64le), resolved against gcr.io's registry API; re-resolve with
# "docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot"
# if this needs refreshing; that command reports the index digest below.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build /out/wishlist /usr/local/bin/wishlist

# Matches the public listener and govue's own admin listener (health,
# readiness, metrics).
EXPOSE 8080
EXPOSE 8081

# Numeric, not "nonroot:nonroot": the same uid/gid the base image's own
# nonroot user already resolves to, redundant but kept explicit.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/wishlist"]
