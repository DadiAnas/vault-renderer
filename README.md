# vault-renderer

[Website](https://dadianas.github.io/vault-renderer/) · [Releases](https://github.com/dadianas/vault-renderer/releases) · [Annotations](ANNOTATIONS.md) · [Examples](examples/multi-workload/targets.yaml) · [License](LICENSE) · [Roadmap](ROADMAP.md)

`vault-renderer` is a standalone Go [Helm post-renderer](https://helm.sh/docs/topics/advanced/#post-rendering) for selected Kubernetes workloads. It generates Vault Agent configuration in a ConfigMap and adds an init container, a sidecar, or both. Vault retrieves secrets when the Pod runs; the renderer does not contact Vault.

**Status:** offline Helm 2/3/4 rendering and local Kubernetes/Vault flows, including private-CA HTTPS, have been tested. Broader annotation compatibility and deployment in the intended environment remain open. See [ROADMAP.md](ROADMAP.md) for release gates.

## Build and use

Prebuilt Linux, macOS, and Windows archives are available on the [Releases page](https://github.com/dadianas/vault-renderer/releases). Verify the archive against its matching line in `checksums.txt` before use. To build from source:

```sh
go test ./...
go build -o vault-renderer ./cmd/vault-renderer
vault-renderer validate --config examples/basic/vault-values.yaml
vault-renderer inspect --config examples/basic/vault-values.yaml
```

## Helm 3

For direct Helm use, the renderer reads configuration from the environment. Helm does not pass chart values or the release namespace to a post-renderer automatically. The direct executable form requires **Helm 3.1+**. [Helm 4 requires a `postrenderer/v1` plugin name](https://docs.helm.sh/docs/overview/#post-renderers-implemented-as-plugins); it cannot use a bare executable path.

```sh
export VAULT_RENDERER_CONFIG="$PWD/examples/basic/vault-values.yaml"
helm template payment examples/basic/chart --namespace production --post-renderer "$PWD/vault-renderer"
# After reviewing the rendered manifests:
helm upgrade --install payment examples/basic/chart --namespace production --post-renderer "$PWD/vault-renderer"
```

## Helm 4

With Helm 4, build the binary into your `PATH`, install the optional [plugin manifest](helm4-plugin/plugin.yaml), and use its name:

```sh
go build -o vault-renderer ./cmd/vault-renderer
export PATH="$PWD:$PATH"
helm plugin install "$PWD/helm4-plugin"
export VAULT_RENDERER_CONFIG="$PWD/examples/basic/vault-values.yaml"
helm template payment examples/basic/chart --namespace production --post-renderer vault-renderer
```

The plugin is needed only for Helm 4 integration. The renderer itself has no Helm runtime dependency and remains a plain stdin/stdout executable. This version boundary means the specification's no-plugin direct invocation is achievable in Helm 3, but not in Helm 4.

## Helm 2

[Helm 2 has no native post-renderer](https://v2.helm.sh/docs/helm/). Render the original Helm 2 chart, pipe it through the Go renderer, and package the result as a normal Helm 2 chart. The source chart is untouched. Use a **new output directory for every revision**:

```sh
helm template ./third-party-chart --name payment --namespace production -f chart-values.yaml \
  | vault-renderer helm2-chart --config vault-values.yaml --namespace production \
      --output-dir ./rendered-payment-001
helm upgrade --install payment ./rendered-payment-001 --namespace production
```

For an upgrade, re-render with the same release name and add `--is-upgrade` to `helm template`; package into `rendered-payment-002`, then `helm upgrade payment ./rendered-payment-002`. Roll back with Helm 2's normal `helm rollback payment REVISION`. The generated chart stores the processed manifests as a file and reads them with `.Files.Get`, so Helm does not evaluate Vault's `{{ ... }}` template expressions a second time. Helm 2 supports v1 source charts; the rendered chart records baked manifests rather than the original chart and values. Keep the original chart, values, and Vault config in source control for reproducible upgrades. This workflow requires a working Helm 2/Tiller installation for deployment.

For standalone use: `vault-renderer render --config FILE --namespace production < manifests.yaml`. Without a subcommand, it renders. `--dry-run` validates the rendered stream and writes no manifests. `--debug` or `--log-level debug` writes a byte count to stderr. Rendering writes only YAML to stdout. Unknown config keys and unsupported injection settings always fail, including when `enabled: false`; `--strict=false` is deliberately rejected.

## Configuration

See the [basic example](examples/basic/vault-values.yaml) and [multi-workload example](examples/multi-workload/targets.yaml). `vault-injector.enabled` must be true. Injection needs a target with `name`, `labelSelector`, or `annotationSelector`, explicit `annotationDiscovery: true`, or explicit `mode: all`. Targets can specify `kind`, `kinds`, `namespace`, and selectors on workload metadata. Kubernetes label `matchLabels` and `matchExpressions` are supported. `exclude` wins over targets. A target that specifies only `kind` is rejected to prevent accidental broad injection. Global mode skips `kube-system`, `kube-public`, and `kube-node-lease`; an explicit target can still select them. Namespace matching uses `--namespace` or `VAULT_RENDERER_NAMESPACE` when `metadata.namespace` is absent; it never assumes `default`.

`VAULT_RENDERER_NAMESPACE` is **optional** for the basic example because its target has no namespace filter. Set it when a chart omits `metadata.namespace` and you use a target or exclusion with an exact `namespace`. This makes the renderer match against the effective Helm release namespace; it does not change the workload manifest's namespace field.

## Multi-workload example and result

The [target configuration](examples/multi-workload/targets.yaml) selects a Deployment by exact name and namespace, a StatefulSet by name, a DaemonSet by label expression, a Job through native Vault annotation keys, a CronJob by name, and a Pod by annotation selector. It excludes a legacy DaemonSet even though that DaemonSet matches the label selector. Init-only and sidecar-only modes are included.

```sh
./vault-renderer render \
  --config examples/multi-workload/targets.yaml \
  --namespace production \
  < examples/multi-workload/manifests.yaml \
  > rendered.yaml
```

The [full rendered result](examples/multi-workload/rendered.yaml) contains six generated ConfigMaps and the eight original resources. For `payment-api`, the generated ConfigMap is `payment-api-deployment-vault-agent` in `production`; it holds `agent-init.hcl` and `agent.hcl`. The Deployment Pod template gains `vault-agent-init`, `vault-agent`, the ConfigMap and projected-token mounts, a memory-backed `vault-secrets` volume, and a checksum. The ConfigMap contains no retrieved secret values. See [ANNOTATIONS.md](ANNOTATIONS.md) for every accepted key and its behavior.

**Application consumption:** injection makes files available at the mount path; it does not rewrite the application's configuration or turn files into process environment variables. Set chart values or application options to read the mounted file when the application needs its contents.

Settings merge in this order: built-in defaults, global `defaults`, existing Pod template Vault annotations, then matching targets. More specific targets override broader ones. Conflicting values from equally specific targets fail. `inject` accepts annotation suffixes or `annotations` with full `vault.hashicorp.com/` keys. Set `annotationDiscovery: true` to select workloads with `vault.hashicorp.com/agent-inject: "true"` on their Pod templates even when there are no targets; it is disabled by default. Secret IDs are arbitrary nonempty suffixes; templates are stored verbatim in HCL and evaluated by Vault Agent. `agent-inject-file-ID` must be a single safe filename under the mount path. `agent-inject-containers` is a comma-separated list of application container names; if omitted, the secret volume is mounted into every application container in the selected workload.

Generated ConfigMap names include workload kind to avoid collisions. The Pod template receives a renderer marker and a SHA-256 checksum of the generated Agent HCL. A repeated render with the same configuration is byte-stable. Existing renderer markers are checked against the ConfigMap and injected artifacts. A collision with an existing ConfigMap or container/volume/mount fails. The renderer removes active `vault.hashicorp.com/*` Pod annotations after consuming them and adds `agent-inject-status: injected`, so a vault-k8s 1.7.6 admission injector does not inject the Pod again. Keep the renderer enabled on Helm upgrades and rollbacks.

## Startup and security

- Default: init container renders initial templates; sidecar continues refresh and renewal. `agent-pre-populate-only: "true"` selects init-only. `agent-pre-populate: "false"` selects sidecar-only; application startup then **does not wait** for secrets. Init uses `exit_after_auth = true` and the Agent template stanzas; the sidecar uses `false`.
- A projected ServiceAccount token is mounted only into Agent containers at `/vault/token/token`. The default audience is `vault` and expiration is 3600 seconds. Configure the Vault Kubernetes auth role to accept that audience. Existing application ServiceAccount automount settings are preserved.
- Secret files use a memory-backed `emptyDir`. Application mounts are read-only. Agent runs as UID/GID 1000 by default with privilege escalation disabled, all capabilities dropped, and a read-only root filesystem. The renderer adds `fsGroup: 1000` when the Pod has none, so application containers can read default `0640` files. If an existing `fsGroup` is set, the Agent adopts it. Explicit `agent-run-as-group` conflicting with it fails.
- HTTPS Vault addresses are required. TLS verification is on by default. For a private CA, set `tls-secret` to a Kubernetes Secret name and `tls-ca-cert` to its CA filename. The Secret is mounted only into Agent containers. The renderer does not create Vault roles, policies, auth mounts, Kubernetes RBAC, or retrieved secret values.
- `allow-http: "true"` permits HTTP only when explicitly configured, for a disposable local Vault dev server. Agent CPU and memory requests/limits default to the official injector values; `agent-requests-*` and `agent-limits-*` allow overrides. `template-static-secret-render-interval` controls KV refresh cadence.

Before deployment, configure Vault's Kubernetes auth mount, a role bound to the workload ServiceAccount and namespace, policies for each template's secret paths, the projected token audience, and the Kubernetes TokenReview reviewer prerequisites. See [Vault Kubernetes auto-auth](https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth/methods/kubernetes) and [Vault Agent templates](https://developer.hashicorp.com/vault/docs/agent-and-proxy/agent/template).

## Compatibility matrix

Reference: [Vault Agent Injector annotations](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/injector/annotations), pinned to **vault-k8s 1.7.6**. This is a bounded compatibility claim, not a complete reimplementation.

| Annotation suffix / feature | Status | Notes |
| --- | --- | --- |
| `role`, `service`, `agent-image`, `agent-inject-secret-*`, `agent-inject-template-*`, `agent-inject-file-*`, `agent-inject-perms-*`, `agent-inject-containers`, `agent-requests-*`, `agent-limits-*`, `template-static-secret-render-interval` | Supported | Native Pod annotations or external config; `service` maps to Vault address. Resource suffixes: `cpu`, `mem`, `ephemeral`. |
| `agent-pre-populate`, `agent-pre-populate-only`, `agent-inject` | Supported | Init-only, init+sidecar, sidecar-only, disabled. |
| `agent-inject-status` | Coexistence only | Renderer outputs `injected`; preexisting official injection is rejected. |
| `auth-path`, `vault-addr`, `vault-namespace`, `token-audience`, `token-expiration`, `mount-path`, `volume-name`, `tls-secret`, `tls-ca-cert`, `agent-run-as-user`, `agent-run-as-group`, `allow-http` | Renderer settings | Some are custom configuration keys rather than official annotation names. |
| Vault Agent cache, retries, timeouts, custom security context, custom PodSpec paths, Helm wrapper | Pending | Tracked in phase 2; unknown keys fail. |
| Token injection, extra secrets, JSON patches, command execution hooks, TLS skip verify | Unsupported | Rejected rather than approximated. |

The official injector runs at Pod admission and supports many more annotations. This renderer processes the six built-in workload kinds only: Deployment, StatefulSet, DaemonSet, Job, CronJob, and Pod. It does not inspect custom resources. [Local qualification](QUALIFICATION.md) covers Vault Agent startup, Kubernetes auth, file rendering, refresh, three startup modes, injector coexistence, Helm rollback, and private-CA HTTPS acceptance and rejection. It does not establish production release admission.
