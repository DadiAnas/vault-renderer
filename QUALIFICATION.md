# Local qualification — 2026-11-02

This is evidence from an isolated kind cluster. It is not a production admission record.

| Component | Exact version |
| --- | --- |
| kind | 0.33.0 |
| Kubernetes node | v1.37.0 |
| Helm client | 4.2.4 |
| Vault Helm chart | 0.31.0 |
| Vault server and Agent | 1.20.4 |
| vault-k8s injector | 1.7.6 |

Vault ran in disposable dev mode in namespace `vault-renderer-test`. The renderer used explicit `allow-http: "true"` for this test. The Vault role was bound to the `payment-api` ServiceAccount, namespace, and `vault` token audience; its policy allowed only the test KV path. No privileged RBAC was created by the renderer.

## Observed

1. Initial install exposed a real failure: the Vault image entrypoint attempted `chown` and capability setup under the restricted non-root security context. Revision 1 failed. The renderer now sets `command: ["/bin/vault"]` for both Agent containers, bypassing that entrypoint. A regression test covers the generated command.
2. Revision 2 deployed with `--wait`: the application Pod reached 2/2 Running with exactly one `vault-agent-init` and one `vault-agent` sidecar. The application container read the expected synthetic username and password file.
3. Updating the KV v2 secret caused the file in the running application container to change with `template-static-secret-render-interval: 10s`. A `grep -q` check for the new synthetic value exited 0.
4. Revision 3 switched to init-only. The Pod reached 1/1 Running with `vault-agent-init`, no sidecar, and the current file.
5. Helm rollback to revision 2 produced revision 4 and restored the sidecar. The Pod rolled out successfully, the file remained current, and one renderer ConfigMap existed.
6. Revision 5 switched to sidecar-only. The Pod reached 2/2 Running with no init container, the sidecar, and the current file. The application's start was not gated on secret rendering in this mode.
7. The official injector webhook was installed and active. Processed Pods carried `agent-inject-status: injected`, and the observed container list contained one renderer init container and one sidecar, with no duplicate admission injection.

Helm 2.17 and 3.19 were separately tested for template integration. Helm 2's generated chart preserved Vault template expressions; Helm 3 used the executable directly; Helm 4 used the optional plugin manifest.

## Remaining gates

- Test missing-secret failure behavior, readiness and refresh under Vault outages, and broader annotation compatibility.
- Qualify the exact installed release and rollback in the intended deployment environment.

## Private-CA HTTPS qualification — 2026-11-02

A second isolated kind v0.33.0 / Kubernetes v1.37.0 cluster ran Vault 1.20.4 in standalone file-backed mode with a TLS listener. A one-day local CA signed a server certificate for `vault.vault-renderer-tls.svc`. `openssl verify -purpose sslserver -verify_hostname` accepted the server certificate under the trusted CA and rejected it under a different CA. Vault was initialized and unsealed; the scoped Kubernetes auth role and KV v2 path were configured over a hostname-verified HTTPS port forward. vault-k8s 1.7.6 remained enabled.

The renderer selected `payment-api`, generated one ConfigMap containing Agent HCL/templates, mounted a **CA-only** Secret into the Agent containers, and generated `ca_cert = "/vault/tls/ca.pem"`. The Vault server private-key Secret was not mounted into the application Pod. Helm 4.2.4 installed the release with `--wait`; the Pod reached 2/2 Running and the application read the expected synthetic test file. The official injector did not add duplicate containers.

For the negative test, a different CA was mounted into a new revision. The new Pod stayed in init; Vault Agent logged `x509: certificate signed by unknown authority`, and Helm timed out waiting for the Deployment. The prior trusted-CA Pod remained healthy. Rollback to revision 1 restored the trusted CA and a successful rollout. A subsequent KV v2 update refreshed the file through the TLS sidecar. A ConfigMap data check found neither retrieved test value.

The first Vault chart upgrade in this run hit a Helm 4 server-side apply conflict on the injector webhook's controller-managed `caBundle`. Repeating that chart upgrade with `--server-side=false` succeeded. This was a Vault chart lifecycle issue, separate from the renderer's payment release.

The second cluster, its in-cluster Secrets/PVC, local CA keys, and temporary kubeconfig were deleted after the checks. This local result does not qualify the intended production environment.

## External Compose Vault + kind — 2026-11-02

A third isolated kind v0.33.0 / Kubernetes v1.37.0 cluster used a Vault 1.20.4 dev server from [dev/compose.yaml](dev/compose.yaml), attached to kind's Docker network. Vault was reachable from the Pod through that network; the Kubernetes API server was reachable from Vault at its Docker-network IP with its certificate authority. A scoped reviewer ServiceAccount received `system:auth-delegator` for the test, and Vault's Kubernetes auth role bound the application ServiceAccount, namespace, and `vault` audience. The renderer created no reviewer RBAC.

Helm 4 installed the rendered `payment-api` Deployment with `--wait`. The Pod reached 2/2 Running with one init Agent, the application, and one sidecar Agent. The application read the initial synthetic KV value; after an external Vault update, the sidecar refreshed the file. The generated ConfigMap contained no retrieved value. This qualified the external-Vault network path, not production admission.

The Compose project and kind cluster were removed. The temporary reviewer JWT, Kubernetes CA copy, kubeconfig, and renderer test configuration were deleted. Unrelated Docker containers remained running.

## Annotation discovery and injector coexistence — 2026-11-02

An isolated kind v0.33.0 / Kubernetes v1.37.0 cluster ran the official vault-k8s 1.7.6 admission webhook from Vault chart 0.31.0. The official Grafana Tempo chart v1.24.4 received Vault annotations through its `podAnnotations` values. The renderer was invoked through the Helm 4 post-renderer plugin with **no renderer configuration file**. It generated one Agent ConfigMap and the Tempo Pod was admitted with one `vault-agent-init`, one `vault-agent`, the original Tempo container and volume, a projected token, and `agent-inject-status: injected`. The installed webhook did not add a duplicate Agent.

For comparison, the same Tempo chart and values were installed in a second namespace without the renderer. The official webhook produced one init Agent, one sidecar, and a shared secrets volume. This comparison verifies the supported container pattern and coexistence, not byte-for-byte equivalence with the official injector. The test Vault address was a placeholder and no runtime secret retrieval was claimed. The chart reports itself deprecated.

## Vault Enterprise namespace annotation — 2026-11-02

A no-config renderer test reproduced a `Deployment/webhook-notif` failure with `vault.hashicorp.com/namespace`. The fix accepts that native annotation, keeps the Kubernetes release namespace separate, and writes the Vault namespace into the Kubernetes auto-auth method in both generated Agent HCL files, as in vault-k8s 1.7.6. A target's existing `vault-namespace` alias overrides the rendered annotation; a rendered annotation overrides a default alias.

The official Tempo chart v1.24.4 rendered through the Helm 4 post-renderer plugin with a Vault namespace annotation and no renderer config. Its generated ConfigMap stayed in Kubernetes namespace `observability`, while both Agent HCL files contained `namespace = "engineering/dev"` under `method "kubernetes"`. Vault Agent 1.20.4 started with the generated init HCL in Docker; it reached Kubernetes authentication and reported the expected missing test token. This verifies configuration parsing, not successful Enterprise authentication or secret retrieval. No Vault Enterprise server or the customer's exact chart was available for this run.
