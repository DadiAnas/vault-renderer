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
