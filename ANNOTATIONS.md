# Supported annotations and renderer settings

This is the contract implemented by this repository, checked against Vault Agent Injector **vault-k8s 1.7.6**. It is a supported subset, not a claim of full injector compatibility. Unknown settings fail validation. See the [official annotation reference](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/injector/annotations) for the wider injector feature set.

Use a suffix in a target's <code>inject</code> map:

~~~yaml
inject:
  role: payment-api
  agent-inject-secret-db: secret/data/payment/db
~~~

Or use full native annotation names under <code>inject.annotations</code>:

~~~yaml
inject:
  annotations:
    vault.hashicorp.com/role: payment-api
    vault.hashicorp.com/agent-inject-secret-db: secret/data/payment/db
~~~

Existing <code>vault.hashicorp.com/*</code> annotations on a selected workload's **Pod template** are also read. Helm applies chart defaults and environment values before the renderer runs, so these are the **final rendered annotations**. Values resolve in this order: built-in defaults → renderer global defaults → final Pod template annotations → matching renderer target inject. More specific targets win for ordinary settings; conflicting equally specific values fail. A target can explicitly override an annotated <code>agent-inject: "false"</code> with <code>agent-inject: "true"</code>. Exclusions and matching targets with <code>agent-inject: "false"</code> always disable injection.

## Supported Vault annotation suffixes

| Suffix | Effect | Default or rule |
| --- | --- | --- |
| <code>role</code> | Vault Kubernetes auth role used by Agent. | Required for every injected workload. |
| <code>namespace</code> | Vault Enterprise namespace used by Agent's Kubernetes auto-auth method. | Optional; distinct from the Kubernetes release namespace. |
| <code>service</code> | Vault address; overrides vault-addr. | HTTPS required unless allow-http is explicitly true. |
| <code>agent-image</code> | Image for both Agent containers. | hashicorp/vault:1.20.4; image must provide /bin/vault. |
| <code>agent-inject</code> | Enable or disable injection for a selected workload. | Annotation discovery requires "true" on the Pod template by default. A matching target can override the final annotation. |
| <code>agent-pre-populate</code> | Include an init Agent to render files before app containers start. | "true". "false" gives sidecar-only and **does not gate app startup**. |
| <code>agent-pre-populate-only</code> | Exit after init rendering and omit the sidecar. | "false"; "true" requires agent-pre-populate: "true". |
| <code>agent-init-first</code> | Put the Vault init Agent before existing init containers. | "false"; by default the Vault init Agent follows existing init containers. |
| <code>agent-inject-secret-ID</code> | Vault secret path for arbitrary nonempty identifier ID. | At least one per injected workload. Actual data is read only by Agent in the Pod. |
| <code>agent-inject-template-ID</code> | Consul Template content for the matching secret ID. | If absent, a map template for that path is generated. Expressions are preserved verbatim. |
| <code>agent-inject-default-template</code> | Selects the generated template format when a secret has no custom template. | "map" by default; "json" is also supported. |
| <code>agent-inject-file-ID</code> | Filename under mount-path for the matching secret ID. | Defaults to ID; must be a single safe filename. Duplicate destinations fail. |
| <code>agent-inject-perms-ID</code> | File mode for the matching template destination. | "0640"; four octal digits. |
| <code>agent-inject-containers</code> | Comma-separated application container names receiving the secret mount. | All application containers in the selected PodSpec; unknown names fail. |
| <code>agent-run-as-user</code> | Non-root UID for Agent containers. | 1000; integer 1–65535. |
| <code>agent-run-as-group</code> | GID for Agent containers and default Pod fsGroup. | 1000; must agree with an existing Pod fsGroup. |
| <code>agent-requests-cpu</code>, <code>agent-requests-mem</code> | CPU and memory requests on each Agent container. | 250m, 64Mi. |
| <code>agent-limits-cpu</code>, <code>agent-limits-mem</code> | CPU and memory limits on each Agent container. | 500m, 128Mi. |
| <code>agent-requests-ephemeral</code>, <code>agent-limits-ephemeral</code> | Ephemeral-storage requests and limits on each Agent container. | Unset. Resource values use Kubernetes quantity syntax; an empty string removes that request/limit. |
| <code>template-static-secret-render-interval</code> | Refresh interval for non-leased secrets such as KV v2. | Vault Agent default if omitted; positive duration such as 10s or 5m. |
| <code>agent-inject-status</code> | Marks already injected Pods so the official webhook does not inject twice. | Renderer outputs "injected". It is not an input setting. |

ID is a suffix, not a fixed list of secret names. Multiple IDs and multiline templates are supported. A template may call more than one Vault path; the renderer never evaluates its expressions.

## Renderer-specific settings

These keys are accepted in <code>defaults</code> or a target's <code>inject</code> map. They are **not** all official injector annotations.

| Key | Effect | Default or rule |
| --- | --- | --- |
| <code>vault-addr</code> | Vault URL used in Agent HCL. | Required unless service supplies it. |
| <code>auth-path</code> | Vault Kubernetes auth mount path. | auth/kubernetes. |
| <code>vault-namespace</code> | Alias for the native <code>namespace</code> setting in optional renderer config. | Unset; an explicit target override takes priority over a rendered <code>vault.hashicorp.com/namespace</code> annotation. |
| <code>token-audience</code> | Audience for the projected Kubernetes ServiceAccount token. | vault; bind the Vault role to the same audience. |
| <code>token-expiration</code> | Projected token lifetime in seconds. | 3600; allowed 600–86400. |
| <code>mount-path</code> | Shared memory-backed secret volume path. | /vault/secrets; absolute, clean path. |
| <code>volume-name</code> | Kubernetes name of that secret volume. | vault-secrets; DNS label. |
| <code>tls-secret</code> + <code>tls-ca-cert</code> | Existing Kubernetes Secret containing a CA file, mounted **only into Agent** at /vault/tls/; Agent verifies Vault's certificate with it. | Both required together. Use a CA-only Secret; do not mount the Vault server's private key. |
| <code>allow-http</code> | Explicitly allow an http:// Vault address. | "false"; intended only for disposable local testing. |

No renderer configuration file is needed for discovery. At the optional <code>vault-injector</code> root, <code>enabled</code> defaults to true; <code>targets</code> contains additional selectors; <code>exclude</code> always wins. Annotation discovery is on by default for workloads with a Pod-template <code>vault.hashicorp.com/agent-inject: "true"</code>; set <code>annotationDiscovery: false</code> to turn it off. <code>mode: all</code> explicitly selects all supported workload kinds except standard Kubernetes system namespaces. <code>namePrefix</code> and <code>labels</code> customize generated ConfigMap metadata. Selectors inspect **workload** metadata, while annotation discovery inspects the **Pod template**. Renderer settings come from a separate config file; Helm does not pass its values to a post-renderer directly. A chart that depends on the installed injector's default Vault address needs a <code>service</code> annotation or an optional renderer <code>vault-addr</code> default, because that address cannot be learned from rendered manifests.

<code>VAULT_RENDERER_NAMESPACE</code> (or <code>render --namespace</code>) is optional for targets without a namespace filter. Set it when a chart omits <code>metadata.namespace</code> and a target or exclusion matches an exact release namespace. The renderer never assumes default.

This release namespace selects the Kubernetes namespace for matching and generated ConfigMaps. <code>vault.hashicorp.com/namespace</code> selects the Vault Enterprise namespace for Agent authentication. They are independent.

## Generated result

For Deployment/payment-api in namespace production, the example generates ConfigMap/payment-api-deployment-vault-agent in production. It contains agent-init.hcl and agent.hcl, not fetched values. The Pod template receives the ConfigMap mount, a projected ServiceAccount token for Agent, a memory-backed vault-secrets volume, the selected application mount, Agent containers, and a checksum annotation. [See the full six-kind rendered example](examples/multi-workload/rendered.yaml).

The application must read the mounted file or be configured through chart values to use it. The renderer does not modify application command lines, configuration files, or process environment variables.

## Explicitly unsupported

Agent cache, token sharing, extra-secret mounts, JSON patches, command hooks, TLS skip verification, custom PodSpec paths, and many other official annotations are not implemented. Unknown keys fail rather than silently approximating the official injector. See [ROADMAP.md](ROADMAP.md) for planned work.
