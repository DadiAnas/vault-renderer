# Roadmap

## Current release

- **Outcome:** publish the Go renderer as an open-source project at `github.com/dadianas/vault-renderer`, with a GitHub Pages guide and downloadable cross-platform release. The `v0.1.0-beta.1` preview is published.
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

## Next compatibility work

- Expand supported Vault Agent Injector annotations with tests for each new setting.
- Add custom PodSpec paths and configurable security contexts only when their validation and conflict behavior are defined.
- Qualify failure behavior during missing secrets, Vault outages, and reauthentication.
- Verify installation, upgrade, and rollback in the intended Kubernetes environment before marking a production release.
