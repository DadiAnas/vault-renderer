# vault-renderer

[Website](https://dadianas.github.io/vault-renderer/) · [Releases](https://github.com/dadianas/vault-renderer/releases) · [Annotations](ANNOTATIONS.md) · [Examples](examples/annotation-only/manifests.yaml) · [License](LICENSE) · [Third-party notices](THIRD_PARTY_LICENSES.md) · [Roadmap](ROADMAP.md)

`vault-renderer` is a standalone Go [Helm post-renderer](https://helm.sh/docs/topics/advanced/#post-rendering) for selected Kubernetes workloads. For workloads already using Vault Agent Injector annotations, it applies the pinned HashiCorp injector mutation engine before deployment. Optional target-only configuration keeps the original ConfigMap-based renderer path. Vault retrieves secrets when the Pod runs; the renderer does not contact Vault.

**Main use case:** your organization already uses Vault Agent Injector annotations, but mutating admission webhooks are unavailable or prohibited by its security policy. Add this post-renderer to the Helm deployment. The chart and environment values keep their existing annotations; the Agent mutations become visible in Helm's rendered release before Kubernetes receives it. Kubernetes admission and workload security policies still apply to those manifests.

**Status:** offline Helm 2/3/4 rendering and local Kubernetes/Vault flows, including private-CA HTTPS, have been tested. Native annotation handling is pinned to vault-k8s 1.7.6; the intended environment and every annotation combination have not been qualified. See [ROADMAP.md](ROADMAP.md) for release gates.

## Build and use

Prebuilt Linux, macOS, and Windows archives are available on the [Releases page](https://github.com/dadianas/vault-renderer/releases). Verify the archive against its matching line in `checksums.txt` before use. To build from source:

```sh
go test ./...
go build -o vault-renderer ./cmd/vault-renderer
vault-renderer validate --config examples/basic/vault-values.yaml
vault-renderer inspect --config examples/basic/vault-values.yaml
```

## Helm 3

For an existing chart with supported Vault Agent Injector annotations, no renderer config file is needed. Helm passes the final manifests to the post-renderer; it does not pass the original values file or release namespace separately. The direct executable form requires **Helm 3.1+**. [Helm 4 requires a `postrenderer/v1` plugin name](https://docs.helm.sh/docs/overview/#post-renderers-implemented-as-plugins); it cannot use a bare executable path.

```sh
helm template myapp ./chart --namespace production --post-renderer "$PWD/vault-renderer"
# After reviewing the rendered manifests:
helm upgrade --install myapp ./chart --namespace production --post-renderer "$PWD/vault-renderer"
```

## Helm 4

With Helm 4, build the binary into your `PATH`, install the optional [plugin manifest](helm4-plugin/plugin.yaml), and use its name:

```sh
go build -o vault-renderer ./cmd/vault-renderer
export PATH="$PWD:$PATH"
helm plugin install "$PWD/helm4-plugin"
helm template myapp ./chart --namespace production --post-renderer vault-renderer
```

The plugin is needed only for Helm 4 integration. The renderer itself has no Helm runtime dependency and remains a plain stdin/stdout executable. This version boundary means the specification's no-plugin direct invocation is achievable in Helm 3, but not in Helm 4.

## Existing annotations, no renderer config

The [annotation-only input](examples/annotation-only/manifests.yaml) is a generic Deployment with Pod-template `agent-inject: "true"`, `service`, `role`, and a dynamic secret path. No target or renderer config file is needed:

```sh
./vault-renderer < examples/annotation-only/manifests.yaml > rendered.yaml
```

The [rendered result](examples/annotation-only/rendered.yaml) contains the original Deployment with one init Agent, one sidecar Agent, a projected ServiceAccount token, and a memory-backed secrets volume. HashiCorp's Agent configuration is encoded in the Agent containers' `VAULT_CONFIG` environment value; no new ConfigMap is needed. `agent-inject-status: injected` prevents a second mutation by an installed webhook. The renderer leaves application containers and other workload settings intact. `VAULT_CONFIG` contains instructions and template expressions, not retrieved secret values.

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

See the [basic example](examples/basic/vault-values.yaml) and [multi-workload example](examples/multi-workload/targets.yaml) for optional configuration. `vault-injector.enabled` defaults to true. By default, the renderer selects supported workloads whose final Pod template has `vault.hashicorp.com/agent-inject: "true"`. Set `annotationDiscovery: false` to turn off that selection. Explicit targets with `name`, `labelSelector`, or `annotationSelector` also select workloads; `mode: all` selects all supported kinds. Targets can specify `kind`, `kinds`, `namespace`, and selectors on workload metadata. Kubernetes label `matchLabels` and `matchExpressions` are supported. `exclude` wins over targets. A target that specifies only `kind` is rejected to prevent accidental broad injection. Global mode skips `kube-system`, `kube-public`, and `kube-node-lease`; an explicit target can still select them. Namespace matching uses `--namespace` or `VAULT_RENDERER_NAMESPACE` when `metadata.namespace` is absent; it never assumes `default`.

### Reuse any Helm values YAML

The renderer reads only a top-level `vault-injector` section and ignores all other chart values. If the section is absent, it uses annotation-discovery defaults, even when the file is passed through `--config` or `VAULT_RENDERER_CONFIG`. The filename is arbitrary, so one file can serve Helm and the renderer:

```yaml
# values.yaml
replicaCount: 2
vault-injector:
  defaults:
    agent-image: registry.company.example/security/vault:2.0.4
```

**Helm 3 — inline post-renderer argument:**

```sh
helm upgrade --install myapp ./chart --namespace production \
  -f values.yaml --post-renderer ./vault-renderer \
  --post-renderer-args=--config=values.yaml
```

**Helm 3 — environment variable:**

```sh
VAULT_RENDERER_CONFIG=values.yaml \
  helm upgrade --install myapp ./chart --namespace production \
    -f values.yaml --post-renderer ./vault-renderer
```

**Helm 4 — install the bundled plugin, then choose either form:**

```sh
export PATH="$PWD:$PATH"
helm plugin install "$PWD/helm4-plugin"

# Inline argument
helm upgrade --install myapp ./chart --namespace production \
  -f values.yaml --post-renderer vault-renderer \
  --post-renderer-args=--config=values.yaml

# Environment variable
VAULT_RENDERER_CONFIG=values.yaml \
  helm upgrade --install myapp ./chart --namespace production \
    -f values.yaml --post-renderer vault-renderer
```

The path must reach both Helm and the renderer because Helm passes only rendered manifests to a post-renderer, not its `-f` arguments or merged values. Relative paths such as `values.yaml` resolve from the command's working directory. The `vault-injector` section is validated strictly when present; unrelated chart values are ignored. A chart with a closed `values.schema.json` may reject a new top-level key before the renderer runs.

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

The [full rendered result](examples/multi-workload/rendered.yaml) demonstrates the optional target-only path: six generated ConfigMaps and the eight original resources. For `payment-api`, the generated ConfigMap is `payment-api-deployment-vault-agent` in `production`; it holds `agent-init.hcl` and `agent.hcl`. The Deployment Pod template gains `vault-agent-init`, `vault-agent`, the ConfigMap and projected-token mounts, a memory-backed `vault-secrets` volume, and a checksum. The ConfigMap contains no retrieved secret values. See [ANNOTATIONS.md](ANNOTATIONS.md) for native annotation behavior and optional renderer settings.

**Application consumption:** injection makes files available at the mount path; it does not rewrite the application's configuration or turn files into process environment variables. Set chart values or application options to read the mounted file when the application needs its contents.

Helm merges chart defaults and `-f` environment values before calling the renderer, so the **final rendered Pod annotations** are what discovery reads. The renderer does not receive Helm values directly: keep renderer defaults and targets in a separate environment configuration file passed through `VAULT_RENDERER_CONFIG`. Values resolve in this order: renderer defaults, final Pod template annotations, then matching renderer targets. A target can override a chart annotation; `exclude` and a matching target with `agent-inject: "false"` still disable injection. More specific targets override broader ones; conflicting equally specific values fail. `inject` accepts annotation suffixes or `annotations` with full `vault.hashicorp.com/` keys. Dynamic secret IDs and template expressions are passed to the pinned injector engine for native annotated workloads. The target-only path retains its documented filename restrictions.

If an existing annotated chart omits the Vault `service` address, use a small optional renderer configuration:

```yaml
vault-injector:
  enabled: true
  defaults:
    vault-addr: https://vault.example.com
```

For a typical Kubernetes-auth chart, the Pod template renders `agent-inject: "true"`, a Vault role, a secret annotation, and a `service` Vault address. The official `agent-configmap` and other auth modes may have different requirements. If the chart relies on the installed webhook's Vault address instead, supply `vault-addr` in optional renderer defaults; that webhook setting is not present in Helm output and cannot be inferred. An environment-specific target can override an annotation. Unknown annotation names fail together rather than being silently dropped; see [ANNOTATIONS.md](ANNOTATIONS.md).

The target-only path generates ConfigMap names that include workload kind and adds a checksum of Agent HCL. The annotation-driven path uses HashiCorp's pinned Agent mutation engine and adds its own deterministic marker. Both paths are byte-stable on repeated render. Existing target-only markers and injected artifacts are checked; name and mount collisions fail. The annotation-driven path retains the original Vault annotations and adds `agent-inject-status: injected`, so a vault-k8s 1.7.6 admission injector does not inject again. Keep the renderer enabled on Helm upgrades and rollbacks.

If a Helm-rendered manifest repeats an ordinary YAML mapping key, the renderer keeps the last value and emits that key once. Duplicate `vault.hashicorp.com/*` keys fail with line numbers because their order could change injection behavior. The optional renderer configuration file remains strict about duplicate keys.

## Startup and security

- Native annotations follow the pinned injector's init + sidecar default, init-only, sidecar-only, init order, template, cache, token sharing, and security-context settings. The default image is the pinned injector's `hashicorp/vault:2.0.4`; `agent-image` overrides it. An annotated HTTP Vault `service` is accepted as by the official injector; use HTTPS and certificate verification for production. `tls-secret` mounts a Secret only into Agent containers, and `ca-cert` names its CA file path.
- Before admission, Kubernetes has not yet added a ServiceAccount token volume. The renderer adds a projected token for Agent authentication, or uses an explicitly named existing projected token volume. Secret files and Agent token sinks use memory-backed volumes. The official engine may share a Vault token with application containers **only when** `agent-inject-token: "true"` requests it.
- The target-only legacy path continues to use a generated ConfigMap, a projected token with default audience `vault`, Agent UID/GID 1000, a Pod `fsGroup`, and HTTPS unless `allow-http: "true"` is set. Its optional `tls-ca-cert` filename maps to the official `ca-cert` path when combined with native annotations.
- Neither path creates Vault roles, policies, auth mounts, Kubernetes reviewer RBAC, or retrieved secret values in Kubernetes resources. Agent requests and limits can be set with native annotations.

Before deployment, configure Vault's Kubernetes auth mount, a role bound to the workload ServiceAccount and namespace, policies for each template's secret paths, the projected token audience, and the Kubernetes TokenReview reviewer prerequisites. See [Vault Kubernetes auto-auth](https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth/methods/kubernetes) and [Vault Agent templates](https://developer.hashicorp.com/vault/docs/agent-and-proxy/agent/template).

## Compatibility matrix

Reference: [Vault Agent Injector annotations](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/injector/annotations), pinned to **vault-k8s 1.7.6**. The default annotation-driven path calls that version's Agent mutation package, rather than approximating annotation behavior in this repository.

| Annotation suffix / feature | Status | Notes |
| --- | --- | --- |
| All 74 annotation names in vault-k8s 1.7.6, including dynamic `*-ID` and `auth-config-*` keys | Pinned official engine | Used for workloads with `agent-inject: "true"` on the final Pod template. See [the key reference](ANNOTATIONS.md). Unknown future names fail together. |
| `agent-inject-status` | Coexistence | The renderer marks the Pod `injected`; the installed webhook did not add duplicate Agents in kind. |
| Target-only renderer settings without native annotations | Legacy subset | Generates Agent HCL in a ConfigMap. See [the target-only settings](ANNOTATIONS.md#optional-target-only-renderer-settings); advanced official annotations require the native annotation path. |
| Custom resources and PodSpec paths outside the six built-in workload kinds | Not handled | Requires a separately defined Pod template path. |

This renderer processes the six built-in workload kinds only: Deployment, StatefulSet, DaemonSet, Job, CronJob, and Pod. It does not inspect custom resources. The annotation-driven path does not know the installed webhook's private defaults, such as a Vault address omitted from the chart; supply those through optional renderer defaults. [Local qualification](QUALIFICATION.md) covers Vault Agent startup, authentication, file rendering and refresh, webhook coexistence, upgrade and rollback, and private-CA HTTPS acceptance and rejection. It does not establish production release admission or test every annotation combination.
