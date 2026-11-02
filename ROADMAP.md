# Roadmap

## Current release

- **Outcome:** publish the Go renderer as an open-source project at `github.com/dadianas/vault-renderer`, with a GitHub Pages guide.
- **Owner / priority:** maintainers / high.
- **Acceptance evidence:** public repository with a license and clean source history; tests, vet, build, and example render pass; GitHub Pages is live and checked at desktop and mobile widths; limitations are visible in the README and site.
- **Impact map:** module import path, CLI build, README links, examples, public documentation, license, GitHub workflows, Pages configuration, and downstream Helm 2/3/4 instructions. No Kubernetes manifests or stored secrets need migration.
- **Current blocker:** the intended Kubernetes/Vault environment has not been identified for release admission.
- **Next action:** publish the repository and GitHub Pages site, verify the live links and CI, then qualify the exact release in the intended environment.

## Verified locally

- Helm 2.17, 3.19, and 4.2 template integration.
- kind Kubernetes v1.37.0 with Vault 1.20.4 and vault-k8s 1.7.6: authentication, file rendering and refresh, init-only and sidecar-only modes, rollback, and injector coexistence.
- Standalone Vault over private-CA HTTPS: trusted CA succeeds, unrelated CA fails closed, rollback restores service, and retrieved values remain out of generated ConfigMaps. See [QUALIFICATION.md](QUALIFICATION.md).

## Next compatibility work

- Expand supported Vault Agent Injector annotations with tests for each new setting.
- Add custom PodSpec paths and configurable security contexts only when their validation and conflict behavior are defined.
- Qualify failure behavior during missing secrets, Vault outages, and reauthentication.
- Verify installation, upgrade, and rollback in the intended Kubernetes environment before marking a production release.
