# wishlist

Installs [wishlist](https://github.com/ostendorf-cloud/wishlist) on a Kubernetes cluster.

## Prerequisites

- Kubernetes 1.27 or newer.
- Only if `httpRoute.enabled` is set: the Gateway API CRDs and a running `Gateway` with a
  listener that allows this release's namespace. See "External routing" below; this chart
  ships neither.

## Installing

```sh
helm install wishlist charts/wishlist
```

This chart is also published as an OCI artifact, at `oci://ghcr.io/ostendorf-cloud/charts/wishlist`,
versioned in lock-step with every release:

```sh
helm install wishlist oci://ghcr.io/ostendorf-cloud/charts/wishlist --version <version>
```

or pull it without installing:

```sh
helm pull oci://ghcr.io/ostendorf-cloud/charts/wishlist --version <version>
```

## Secrets

The database password is read from a `Secret` the installer already has, never created from
plaintext values here. `database.existingSecret.name` and `database.existingSecret.passwordKey`
name it. The chart mounts the key as a file and passes `DB_PASSWORD_FILE`, so the password is
never an environment value. The rest of the connection (`database.host`, `port`, `name`,
`user`, `sslMode`, `maxConns`, `connectTimeout`) is plain values; the defaults assume a
CloudNativePG cluster named `wishlist-postgres`, bootstrapped without custom names: host
`wishlist-postgres-rw`, database and role `app`, password in the Secret
`wishlist-postgres-app` under the key `password`. Set the values when the cluster differs. The
chart creates no database. `database.sslMode` defaults to `require`, which encrypts and does not
verify the server certificate: verifying it (`verify-full`) needs a root certificate setting
that the service does not have yet.

`/readyz` reports unready while the database is unreachable or the schema is not applied
yet, so a pod without a database receives no traffic. `/healthz` does not depend on it.

## Health, readiness and metrics

`/healthz`, `/readyz` and `/metrics` are served on a second, administrative listener
(`service.adminPort`, default `8081`), never on the public `service.port`:
`templates/deployment.yaml`'s `startupProbe`, `livenessProbe` and `readinessProbe` all point
at the `admin` container port, and `templates/ingress.yaml`/`templates/httproute.yaml` only
ever route to `service.port`. A `ServiceMonitor` or `PodMonitor` (not part of this chart) can
scrape `service.adminPort` directly, in-cluster.

## External routing

This chart reaches external traffic to its own `Service` two ways, chosen by
`ingress.enabled` and `httpRoute.enabled`. Both default to `false`: a default install creates
neither, reachable only from inside the cluster, until one is deliberately turned on. They are
mutually exclusive: turning both on fails the install or upgrade with a readable message
naming which one to turn off, enforced once in `templates/deployment.yaml`, the one template
that always renders regardless of which routing flag is set.

### Ingress

Set `ingress.enabled: true`. `ingress.hosts` names the host and path rules, and `ingress.tls`
carries the certificate configuration directly on the `Ingress` object: this chart owns that
configuration.

### Gateway API HTTPRoute

Set `httpRoute.enabled: true`, `httpRoute.parentRefs.name` and `httpRoute.parentRefs.namespace`
to the `Gateway` this release attaches to. The namespace is required, not optional the way
`ingress.className` is: a `Gateway` is almost always platform owned and lives in a different
namespace than this release, and the Gateway API's own default for an unset
`parentRefs.namespace` is the `HTTPRoute`'s own namespace, normally wrong here.

This chart never creates the `Gateway` and never ships the Gateway API CRDs (`Gateway`,
`HTTPRoute` and the rest): both are cluster-level prerequisites, installed and operated once
per cluster by the cluster operator, outside this chart. The `Gateway`'s own `allowedRoutes`
decides whether it actually accepts this `HTTPRoute`.

Unlike `Ingress`, an `HTTPRoute` carries no `tls` field of its own. TLS terminates on the
`Gateway`'s listener instead, which the cluster operator, not this chart, owns and configures.

Routing itself is fixed to this chart's own `Service`, on `service.port`, never
`service.adminPort`: no values-driven way to add a second backend or a per-path rule, the same
single-backend assumption `ingress.hosts` already makes.

### Trusted proxies

Behind an `Ingress` or an `HTTPRoute` every request reaches the pod from the proxy. With no
trusted proxy the sign in limits would key by the proxy's address, and all callers would share
one bucket. So the render fails when `ingress.enabled` or `httpRoute.enabled` is true and
`auth.trustedProxies` is empty. Set it to the pod CIDR of the ingress controller or gateway.

### Registration

`auth.registrationOpen` is `false` by default: the signup route answers 403 and creates
nothing. Open it only while somebody has to register. An account that never confirms its TOTP
secret is deleted at the next signup that comes 24 hours or more after its creation, unless it
already owns a list.

### Replicas

`replicaCount` is at most `1`: the sessions live in the memory of the process.
`values.schema.json` (`maximum: 1`) and a guard in `templates/deployment.yaml` both refuse a
higher value.

## Values

See `values.yaml` for the full, commented list. The ones most deployments touch:

| Key | Description | Default |
| --- | --- | --- |
| `image.repository` | Container image | `ghcr.io/ostendorf-cloud/wishlist` |
| `image.tag` | Image tag | `.Chart.AppVersion` |
| `replicaCount` | Pod replicas. At most `1`: sessions live in the memory of the process, a higher value fails the render. Scaling out needs a shared session store in the service first. | `1` |
| `auth.registrationOpen` | Anybody can create an owner account. `true` opens registration (`REGISTRATION_OPEN`). Closed by default, like the binary. Only docker-compose sets it open, for local development. Accounts whose TOTP secret is not confirmed are deleted at the next signup after 24 hours | `false` |
| `auth.trustedProxies` | Proxy addresses or CIDR ranges whose `X-Forwarded-For` is believed (`TRUSTED_PROXIES`). Required when `ingress.enabled` or `httpRoute.enabled` is true: the render fails with it empty | `[]` |
| `service.port` | Public Service and container port, the only one the `Ingress`/`HTTPRoute` ever route to | `8080` |
| `service.adminPort` | govue's admin listener: health, readiness and metrics, in-cluster only | `8081` |
| `ingress.enabled` | Create an `Ingress`. Mutually exclusive with `httpRoute.enabled`; see "External routing" above. | `false` |
| `ingress.annotations` | Annotations for the `Ingress` | `{}` |
| `ingress.tls` | TLS configuration for the `Ingress` | `[]` |
| `httpRoute.enabled` | Create a Gateway API `HTTPRoute`. Mutually exclusive with `ingress.enabled`; see "External routing" above. | `false` |
| `httpRoute.hostnames` | Hostnames the `HTTPRoute` matches | `[]` |
| `httpRoute.parentRefs.name` | Name of the `Gateway` to attach to. Required when `httpRoute.enabled` is true | `""` |
| `httpRoute.parentRefs.namespace` | Namespace the `Gateway` lives in. Required when `httpRoute.enabled` is true | `""` |
| `resources` | Pod resource requests/limits | see `values.yaml` |

## Upgrading

No breaking change has shipped yet; `Chart.yaml`'s `version` tracks that going forward.

## Uninstalling

```sh
helm uninstall wishlist
```

Removes the `Deployment`, `Service`, `ServiceAccount`, and the `Ingress` or `HTTPRoute`,
whichever was enabled, all created by this chart.

## Verification

This chart is verified with:

- `helm lint`
- `helm template`, for the default values and for the documented ingress-enabled and
  httpRoute-enabled combinations.
- `kubeconform`, against the rendered manifests. The default schema set carries no Gateway API
  schemas, so an httpRoute-enabled render's `HTTPRoute` is validated with a second
  `-schema-location` added after the default one, pointing at the community
  [CRDs-catalog](https://github.com/datreeio/CRDs-catalog):

  ```sh
  kubeconform -strict -summary \
    -schema-location default \
    -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
  ```

- [`helm-unittest`](https://github.com/helm-unittest/helm-unittest), run with
  `helm unittest charts/wishlist`; see `mise.toml` for how the plugin is pinned.

`task lint:helm`, `task validate:kubeconform` and `task test:helm` run each of these, one job
per check in CI.
