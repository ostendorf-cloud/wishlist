#!/usr/bin/env bash
#
# Starts the container image given as $1 and asserts it actually serves
# a request: health, readiness and metrics on the admin listener, and
# the embedded SPA shell on the public one. Run by the docker-build job
# in .github/workflows/checks.yaml right after it builds and loads the
# image, so this never needs a registry.
#
# No environment variable is passed to the container: this scaffold has
# no configuration surface beyond the listen addresses and the log
# level, every one of which has a usable default; see
# cmd/wishlist/main.go and internal/config.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <image-ref>" >&2
  exit 1
fi

image="$1"
container="wishlist-smoke-$$"
base_url="http://127.0.0.1:8080"
admin_url="http://127.0.0.1:8081"
ready_timeout=30
fail=0

cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    echo "---- ${container} logs ----" >&2
    docker logs "$container" 2>&1 || true
    echo "---- end ${container} logs ----" >&2
  fi
  docker rm --force "$container" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT

docker run \
  --detach \
  --name "$container" \
  --publish "127.0.0.1:8080:8080" \
  --publish "127.0.0.1:8081:8081" \
  "$image" >/dev/null

# wait_for_health polls /healthz on the admin listener for up to
# ready_timeout seconds, failing immediately once the container itself
# has exited rather than waiting out the full timeout for a process that
# is already gone.
wait_for_health() {
  local elapsed=0 state exit_code
  while ((elapsed < ready_timeout)); do
    state="$(docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null || echo false)"
    if [[ $state != "true" ]]; then
      exit_code="$(docker inspect --format '{{.State.ExitCode}}' "$container" 2>/dev/null || echo unknown)"
      echo "${container} exited early with code ${exit_code}" >&2
      return 1
    fi
    if curl --fail --silent --show-error --max-time 2 "${admin_url}/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  echo "timed out after ${ready_timeout}s waiting for /healthz" >&2
  return 1
}

if ! wait_for_health; then
  exit 1
fi

# assert_status records a failure and keeps going, checking every route
# rather than stopping at the first failure. It defaults to base_url, the
# public listener; a third argument points at the admin listener instead.
assert_status() {
  local path="$1" want="$2" url="${3:-$base_url}" got
  got="$(curl --silent --output /dev/null --write-out '%{http_code}' "${url}${path}")" || got=""
  if [[ $got != "$want" ]]; then
    echo "GET ${url}${path}: want status ${want}, got \"${got}\"" >&2
    fail=1
  fi
}

assert_status "/healthz" "200" "$admin_url"
assert_status "/readyz" "200" "$admin_url"
assert_status "/metrics" "200" "$admin_url"

# The public listener must never answer /metrics: that stays on the admin
# listener above, cluster-internal, never reached through the chart's own
# Ingress or HTTPRoute.
assert_status "/metrics" "404"

assert_status "/" "200"

# http.FileServerFS canonicalises any request for /index.html to / with a
# 301 redirect; that is correct, standard net/http behaviour, not a bug.
assert_status "/index.html" "301"

if [[ $fail -ne 0 ]]; then
  echo "smoke test failed" >&2
  exit 1
fi

echo "smoke test passed"
