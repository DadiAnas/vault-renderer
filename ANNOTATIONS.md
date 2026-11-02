# Vault annotations and renderer settings

The main use case is an existing chart with Vault Agent Injector annotations in an environment where mutating admission is unavailable or prohibited by security policy. For a workload whose final Pod template contains `vault.hashicorp.com/agent-inject: "true"`, the renderer uses the **pinned vault-k8s 1.7.6 Agent mutation package** before deployment. Kubernetes admission and workload security policies still apply to the rendered manifests.

The tables below cover that version's annotation names; each key is prefixed with `vault.hashicorp.com/`. Dynamic `-ID` and `-PARAMETER` names require a nonempty suffix. [HashiCorp's annotation reference](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/injector/annotations) describes each value in greater depth. Unknown future annotation names are reported together before mutation.

Helm applies chart defaults and environment values before the renderer sees the manifests. Renderer defaults are applied first, final Pod annotations next, and matching external target overrides last. `exclude` and a target with `agent-inject: "false"` disable injection. This precedence applies to native annotations without trying to discover which values file produced them.

The renderer reads the Pod template at `spec.template.metadata.annotations` for Deployments, StatefulSets, DaemonSets, and Jobs; `spec.jobTemplate.spec.template.metadata.annotations` for CronJobs; and `metadata.annotations` for Pods. It processes no custom PodSpec paths.

## Selection, Agent containers, and resources

| Annotation suffix | Effect |
| --- | --- |
| `agent-inject` | Selects or disables mutation. `"true"` selects a workload by default. |
| `agent-inject-status` | `injected` prevents a second mutation by the installed webhook. The renderer sets it after mutation. |
| `agent-request-namespace` | Kubernetes request namespace used by the injector engine; distinct from Vault Enterprise `namespace`. |
| `agent-image` | Image for the Agent containers. The pinned injector default is `hashicorp/vault:2.0.4`. |
| `agent-pre-populate`, `agent-pre-populate-only`, `agent-init-first` | Control init + sidecar, init-only, sidecar-only, and placement relative to existing init containers. |
| `agent-inject-containers` | Comma-separated application container names that receive the secret mount. |
| `agent-run-as-user`, `agent-run-as-group`, `agent-run-as-same-user` | Control Agent UID/GID or use the first application container's UID. |
| `agent-set-security-context`, `agent-share-process-namespace` | Control the Agent security context and Pod process namespace. |
| `agent-copy-volume-mounts` | Copy a named application's volume mounts into Agent containers. |
| `agent-requests-cpu`, `agent-requests-mem`, `agent-requests-ephemeral` | Set Agent resource requests. |
| `agent-limits-cpu`, `agent-limits-mem`, `agent-limits-ephemeral` | Set Agent resource limits. |
| `agent-json-patch`, `agent-init-json-patch` | Apply HashiCorp's JSON patch to the sidecar or init Agent container. The renderer rejects duplicate resulting container names or mount paths. |

## Vault connection and authentication

| Annotation suffix | Effect |
| --- | --- |
| `service` | Vault URL. If omitted, supply `vault-addr` in optional renderer defaults because Helm output cannot reveal the installed webhook's configured address. |
| `namespace` | Vault Enterprise namespace for the Agent's auth method; separate from the Kubernetes release namespace. |
| `role` | Kubernetes auth role, unless a different supported auth mode/config supplies its requirements. |
| `auth-type`, `auth-path`, `auth-config-PARAMETER` | Select the Vault auth method, mount path, and method-specific parameters. |
| `auth-min-backoff`, `auth-max-backoff`, `agent-auto-auth-exit-on-err` | Control authentication retry timing and exit behavior. |
| `agent-service-account-token-volume-name` | Reuse a named projected ServiceAccount token volume. Otherwise the renderer adds one before admission. |
| `tls-secret` | Mount an existing Kubernetes TLS Secret into Agent containers at `/vault/tls`. |
| `ca-cert`, `ca-key`, `client-cert`, `client-key`, `tls-server-name`, `tls-skip-verify` | Set the Agent's CA path, CA directory, client certificate/key, server name, and TLS verification behavior. `tls-skip-verify` disables certificate verification when explicitly set. |
| `proxy-address`, `client-max-retries`, `client-timeout` | Set Vault proxy and HTTP client retry/timeout behavior. |

## Secret files, templates, and Agent runtime

| Annotation suffix | Effect |
| --- | --- |
| `agent-inject-secret-ID` | Vault path for a dynamically named secret file. |
| `agent-inject-template-ID`, `agent-inject-template-file-ID`, `agent-inject-default-template` | Inline template, template file from an extra Secret, or `map`/`json` default template. |
| `agent-inject-file-ID`, `agent-inject-perms-ID`, `secret-volume-path`, `secret-volume-path-ID` | Destination name, permissions, and shared or per-secret mount paths. |
| `agent-inject-command-ID`, `error-on-missing-key-ID`, `preserve-secret-case`, `preserve-secret-case-ID` | Post-render command, missing-key handling, and secret-name case handling. |
| `agent-template-left-delim-ID`, `agent-template-right-delim-ID` | Custom delimiters for an individual template. |
| `agent-configmap`, `agent-extra-secret` | Mount an existing Agent ConfigMap or an extra Kubernetes Secret. The default annotation path does not create a ConfigMap. |
| `agent-inject-token` | Share the Vault token with application containers in the secret volume. |
| `agent-cache-enable`, `agent-cache-use-auto-auth-token`, `agent-cache-listener-port`, `agent-cache-exit-on-err` | Configure Agent cache and its listener/persistence behavior. |
| `agent-enable-quit`, `agent-revoke-on-shutdown`, `agent-revoke-grace` | Enable quit and token revocation controls. |
| `agent-disable-idle-connections`, `agent-disable-keep-alives` | Change Agent connection reuse for selected features. |
| `template-config-exit-on-retry-failure`, `template-static-secret-render-interval`, `template-max-connections-per-host`, `template-lease-renewal-threshold` | Tune template failure, refresh, connection, and lease behavior. |
| `agent-telemetry-PARAMETER`, `go-max-procs`, `log-level`, `log-format` | Configure Agent telemetry, Go scheduling, and logs. |

The renderer keeps these native annotations and adds `agent-inject-status: injected`. The pinned engine places its Agent configuration and template expressions in the Agent containers' encoded `VAULT_CONFIG` environment value. Retrieved Vault values remain in Pod memory-backed files. The renderer never fetches them during Helm execution. [The annotation-only example](examples/annotation-only/manifests.yaml) and its [rendered output](examples/annotation-only/rendered.yaml) show this path.

## Optional target-only renderer settings

Defaults, exclusions, overrides, or targets can live in a standalone YAML file **or in any Helm values YAML** under a top-level `vault-injector` key. Pass that file with `--config=PATH`, `--post-renderer-args=--config=PATH`, or `VAULT_RENDERER_CONFIG=PATH`; Helm's `-f` flag alone does not forward its file path to the post-renderer. The renderer ignores unrelated chart values and strictly validates only its own section. When a workload already has `agent-inject: "true"`, matching targets override native annotations and the official engine is used. A target-only workload without that native annotation keeps the legacy ConfigMap-based path, which supports the narrower set below. Advanced official annotations on a target-only workload fail rather than being ignored.

| Key or family | Target-only behavior |
| --- | --- |
| `role`, `service`, `agent-image`, `agent-inject` | Vault role/address, Agent image, enable/disable. |
| `agent-pre-populate`, `agent-pre-populate-only`, `agent-init-first` | Startup mode and init order. |
| `agent-inject-secret-ID`, `agent-inject-template-ID`, `agent-inject-file-ID`, `agent-inject-perms-ID` | Secret path, inline template, a single safe destination filename, and file mode. |
| `agent-inject-containers`, `agent-run-as-user`, `agent-run-as-group` | Application mounts and Agent identity. |
| `agent-requests-*`, `agent-limits-*`, `template-static-secret-render-interval` | Resources and static-secret refresh. |
| `vault-addr`, `auth-path`, `vault-namespace`, `token-audience`, `token-expiration`, `mount-path`, `volume-name` | Renderer-specific address/auth, Vault namespace alias, projected-token, and volume defaults. |
| `tls-secret` + `tls-ca-cert` | Existing CA-only Secret mounted into Agents at `/vault/tls`; the latter is a safe filename. |
| `allow-http` | Permits an HTTP Vault address in this legacy path when explicitly true. |

At the `vault-injector` config root, `enabled` defaults to true; `annotationDiscovery` defaults to true; `targets`, `exclude`, `mode: all`, `namePrefix`, and `labels` control selection and generated ConfigMap metadata. `exclude` always wins. `VAULT_RENDERER_NAMESPACE` or `render --namespace` is needed only for an exact Kubernetes namespace selector when a chart omits `metadata.namespace`. It never sets the Vault Enterprise namespace. [The multi-workload example](examples/multi-workload/targets.yaml) and its [result](examples/multi-workload/rendered.yaml) show this path.

The application must be configured to read the mounted file if it needs its contents. Neither engine rewrites application configuration or turns rendered files into process environment variables. [Local qualification](QUALIFICATION.md) records which behaviors were exercised with Vault and Kubernetes; use it alongside this API inventory rather than treating every possible combination as live-tested.
