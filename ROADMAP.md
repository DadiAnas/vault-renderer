# Roadmap

## Current release

- **Outcome:** publish the Go renderer as an open-source project at `github.com/dadianas/vault-renderer`, with a GitHub Pages guide and downloadable cross-platform release. The `v0.2.0-beta.1` preview uses the pinned HashiCorp Agent mutation engine for existing annotations.
- **Owner / priority:** maintainers / high.
- **Acceptance evidence:** public repository with a license and clean source history; tests, vet, build, gosec, multi-target fixture, and third-party chart rendering pass; GitHub Pages is live and checked at desktop and mobile widths; a new Compose Vault plus kind Kubernetes test passes; Linux/macOS/Windows binaries and checksums are attached to a verified GitHub release; supported annotations, generic use cases, and example outputs are explained in the README and site.
- **Impact map:** module import path, CLI build, README links, multi-kind fixtures and rendered output, third-party chart qualification, annotation reference, public documentation, license, GitHub workflows, Pages configuration, Compose test fixture, security scanning, release packaging, and downstream Helm 2/3/4 instructions. No Kubernetes manifests or stored secrets need migration.
- **Evidence:** the Go workflow passed for the release commit; gosec v2.30.0 reported zero issues; the `golang.org/x/net` alert is fixed; Pages is live; the six release archives and checksums were verified after upload. Linux amd64/arm64 and macOS arm64 version commands executed. Windows and macOS amd64 were cross-compiled and inspected.
- **Current blocker:** the intended Kubernetes/Vault environment has not been identified for production admission.
- **Next action:** qualify installation, upgrade, rollback, and application consumption in the intended environment. The local kind and Compose tests do not establish production admission.

## Verified locally

- Helm 2.17, 3.19, and 4.2 template integration.
- kind Kubernetes v1.37.0 with Vault 1.20.4 and vault-k8s 1.7.6: authentication, file rendering and refresh, init-only and sidecar-only modes, rollback, and injector coexistence.
- Standalone Vault over private-CA HTTPS: trusted CA succeeds, unrelated CA fails closed, rollback restores service, and retrieved values remain out of generated ConfigMaps. See [QUALIFICATION.md](QUALIFICATION.md).
- External Vault in Docker Compose with a kind workload: Kubernetes auth, initial file rendering, refresh, and no retrieved values in the ConfigMap. See [QUALIFICATION.md](QUALIFICATION.md).

## Annotation discovery default

- **Outcome / owner / priority:** maintainers / high: make annotation-driven injection work with only `--post-renderer`, while retaining environment-specific targets, exclusions, defaults, and an explicit discovery opt-out.
- **Impact map:** CLI optional config loading, configuration defaults, render selection and precedence, existing annotation and target callers, annotation and CLI tests, Helm 2/3/4 integration, README and annotation reference, and the public guide. No manifest schema or stored secrets change.
- **Acceptance evidence:** no-config CLI selects an annotated workload; `annotationDiscovery: false` does not; matching environment targets override supported annotation values including `agent-inject`; exclusions and disable targets still win; unannotated, untargeted resources remain unchanged; Go tests, vet, gosec, and a real annotated chart render pass. An annotation without a Vault address requires an external address because Helm output cannot reveal injector deployment defaults.
- **Evidence:** `v0.1.0-beta.2` is published with six verified archives and checksums. No-config CLI and annotation precedence tests pass; a real Grafana Tempo chart rendered with annotations and no renderer config; an installed vault-k8s 1.7.6 webhook did not add duplicate Agents. The same chart without the renderer received the official init and sidecar pattern in a comparison namespace. CI, vet, and gosec passed; Pages is live. See [QUALIFICATION.md](QUALIFICATION.md).
- **Current blocker / next action:** production admission remains open. Runtime secret retrieval for this particular no-config Tempo run was not tested; application consumption still needs chart-specific configuration. Qualify the exact release in the intended Kubernetes/Vault environment before treating it as production-ready.

## Duplicate keys in Helm manifests

- **Outcome / owner / priority:** maintainers / high: accept repeated noncritical mapping keys such as `appcode` and `opscontact` in Helm-rendered manifests without aborting injection.
- **Impact map:** manifest YAML decoding in `Render`, selected and unselected workload output, duplicate Vault annotation handling, renderer regression tests, and release documentation. External config decoding remains strict.
- **Acceptance evidence:** duplicate ordinary labels use a documented deterministic value, the annotation-driven workload still injects, malformed YAML still fails, and duplicate injection-critical Vault annotations cannot silently change behavior. Run Go tests, vet, gosec, and a CLI render before publishing a fix.
- **Evidence:** the reported duplicate-key error was reproduced in a renderer regression test. Ordinary keys now use the last value; duplicate Vault annotations fail with line numbers; optional config parsing remains strict. Go tests, vet, gosec, and a no-config CLI render with both repeated labels pass. `v0.1.0-beta.3` is published; uploaded archive digests match the verified local checksums.
- **Current blocker / next action:** no blocker for the reported key pattern. The user's exact chart was not provided; qualify that chart and intended environment before production admission.

## Vault Enterprise namespace annotation

- **Outcome / owner / priority:** maintainers / high: accept `vault.hashicorp.com/namespace` on rendered workloads and place it in the Vault Agent Kubernetes auto-auth configuration, matching the pinned injector behavior.
- **Impact map:** annotation validation and precedence, the existing `vault-namespace` alias in optional config, generated Agent HCL and checksums, annotation-only CLI and renderer tests, documentation, release packaging, and downstream Pod startup. No Kubernetes resource migration is needed; a changed Agent HCL checksum can roll selected Pods on upgrade.
- **Acceptance evidence:** a no-config `webhook-notif` Deployment with a native Vault namespace annotation renders successfully; generated init and sidecar HCL set the namespace on the auth method; external target override wins; existing config alias remains supported; Go tests, vet, gosec, and CLI rendering pass.
- **Evidence:** `v0.1.0-beta.4` is published with six verified archives and checksums. The `webhook-notif` regression, real Tempo chart via Helm 4 plugin, Vault Agent 1.20.4 configuration startup, Go tests, vet, gosec, and CI passed. See [QUALIFICATION.md](QUALIFICATION.md).
- **Current blocker / next action:** the exact customer chart and Vault Enterprise server were unavailable locally. Verify the installed preview against those before production admission; Agent configuration parsing alone does not prove Enterprise authentication or secret retrieval.

## Injector annotation compatibility batch

- **Outcome / owner / priority:** maintainers / high: support the full pinned vault-k8s 1.7.6 annotation engine for existing annotated charts, including CA/TLS, auth, cache, templates, token sharing, and JSON patches; reject unknown future annotation keys together. Present the main use case clearly: organizations that cannot use a mutating admission webhook under their security policy.
- **Impact map:** replace the annotation-driven mutation path with HashiCorp's pinned Agent library and JSON patch, preserve target-only legacy rendering, map optional overrides, synthesize a projected token for pre-admission rendering, preserve arbitrary manifest fields, update fixtures, docs, license notices, tests, and release packaging. Annotation-driven output moves from a generated ConfigMap to the official Agent environment configuration; this changes Pod specs and rollout checksums on upgrade.
- **Acceptance evidence:** the full official annotation key inventory is covered; CA/TLS and advanced annotation fixtures render; output is deterministic and idempotent; existing workload fields and container/volume uniqueness are preserved; real chart rendering and local Vault/kind secret retrieval with the official webhook installed pass; Go tests, vet, gosec, and release checksums pass.
- **Evidence:** the 74 pinned annotation names were compared with the HashiCorp source and captured as an offline test fixture. Six workload-kind paths, startup modes, CA/TLS, cache, token sharing, command hooks, and deterministic multi-secret output have regression checks. A local kind cluster with Vault 1.20.4 and vault-k8s 1.7.6 proved secret retrieval and refresh, webhook coexistence, unchanged-upgrade stability, rollback, trusted private-CA HTTPS, and rejection of an unrelated CA. Go tests, vet, gosec, and govulncheck pass with Go 1.26.10 and patched x/net/x/text. See [QUALIFICATION.md](QUALIFICATION.md).
- **Evidence:** `v0.2.0-beta.1` is published with six archive digests verified after upload; its Go CI workflow, including gosec and govulncheck, passed. The generic README, annotation reference, and site now lead with the restricted-admission use case and state that Kubernetes policies still apply.
- **Current blocker / next action:** the exact customer chart and Vault Enterprise server are unavailable locally. Qualify those paths before production admission; the local run covers representative annotation combinations, not every possible combination.

## Next compatibility work

- Track new vault-k8s annotation releases and update the pinned inventory, compatibility tests, and dependency audit together.
- Add custom PodSpec paths and configurable security contexts only when their validation and conflict behavior are defined.
- Qualify failure behavior during missing secrets, Vault outages, and reauthentication.
- Verify installation, upgrade, and rollback in the intended Kubernetes environment before marking a production release.
