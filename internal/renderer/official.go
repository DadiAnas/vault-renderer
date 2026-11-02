package renderer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	agent "github.com/hashicorp/vault-k8s/agent-inject/agent"
	corev1 "k8s.io/api/core/v1"
)

const officialEngine = "vault-renderer.dadianas.com/official"

var officialExact = map[string]bool{}

func init() {
	for _, key := range strings.Fields(`
agent-auto-auth-exit-on-err agent-cache-enable agent-cache-exit-on-err agent-cache-listener-port
agent-cache-use-auto-auth-token agent-configmap agent-copy-volume-mounts agent-disable-idle-connections
agent-disable-keep-alives agent-enable-quit agent-extra-secret agent-image agent-init-first
agent-init-json-patch agent-inject agent-inject-containers agent-inject-default-template
agent-inject-status agent-inject-token agent-json-patch agent-limits-cpu agent-limits-ephemeral
agent-limits-mem agent-pre-populate agent-pre-populate-only agent-request-namespace
agent-requests-cpu agent-requests-ephemeral agent-requests-mem agent-revoke-grace
agent-revoke-on-shutdown agent-run-as-group agent-run-as-same-user agent-run-as-user
agent-service-account-token-volume-name agent-set-security-context agent-share-process-namespace
auth-max-backoff auth-min-backoff auth-path auth-type ca-cert ca-key client-cert client-key
client-max-retries client-timeout go-max-procs log-format log-level namespace
preserve-secret-case proxy-address role secret-volume-path service
template-config-exit-on-retry-failure template-lease-renewal-threshold
template-max-connections-per-host template-static-secret-render-interval tls-secret
tls-server-name tls-skip-verify`) {
		officialExact[key] = true
	}
}

var officialDynamic = []string{
	"agent-inject-command-", "agent-inject-file-", "agent-inject-perms-",
	"agent-inject-secret-", "agent-inject-template-", "agent-inject-template-file-",
	"agent-telemetry-", "agent-template-left-delim-", "agent-template-right-delim-",
	"auth-config-", "error-on-missing-key-", "preserve-secret-case-",
	"secret-volume-path-",
}

func isOfficialAnnotation(key string) bool {
	if officialExact[key] {
		return true
	}
	for _, stem := range officialDynamic {
		if strings.HasPrefix(key, stem) && len(key) > len(stem) {
			return true
		}
	}
	return false
}

func mergeOfficialLayer(out, layer map[string]string) error {
	if value := layer["vault-addr"]; value != "" {
		out["service"] = value
	}
	if value := layer["vault-namespace"]; value != "" {
		out["namespace"] = value
	}
	if value := layer["mount-path"]; value != "" {
		out["secret-volume-path"] = value
	}
	if value := layer["tls-ca-cert"]; value != "" {
		if path.Base(value) != value || value == "." || value == ".." {
			return fmt.Errorf("invalid tls-ca-cert filename")
		}
		out["ca-cert"] = "/vault/tls/" + value
	}
	for key, value := range layer {
		switch key {
		case "vault-addr", "vault-namespace", "mount-path", "tls-ca-cert":
			continue
		}
		out[key] = value
	}
	return nil
}

func injectOfficial(doc, podMeta, podSpec map[string]any, namespace string, settings map[string]string) error {
	annotations := ensureObject(podMeta, "annotations")
	if str(annotations, prefix+"agent-inject-status") == "injected" {
		return nil
	}
	var unknown []string
	for key := range annotations {
		if strings.HasPrefix(key, prefix) && !isOfficialAnnotation(strings.TrimPrefix(key, prefix)) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unsupported Vault annotations: %s", strings.Join(unknown, ", "))
	}

	// Build a synthetic Pod for HashiCorp's pinned mutation engine; apply its patch to
	// the original map so fields unknown to this Kubernetes client remain intact.
	meta := map[string]any{}
	for key, value := range podMeta {
		meta[key] = value
	}
	if str(meta, "name") == "" {
		meta["name"] = str(object(doc, "metadata"), "name")
	}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	ann := map[string]any{}
	for key, value := range annotations {
		ann[key] = value
	}
	for key, value := range settings {
		if isOfficialAnnotation(key) && key != "agent-inject-status" {
			ann[prefix+key] = value
		}
	}
	if settings["volume-name"] != "" && settings["volume-name"] != "vault-secrets" {
		return fmt.Errorf("volume-name is not supported with official annotation injection")
	}
	meta["annotations"] = ann
	volumes := append([]any{}, array(podSpec, "volumes")...)
	if str(ann, agent.AnnotationAgentServiceAccountTokenVolumeName) == "" {
		for _, item := range volumes {
			if volume, ok := item.(map[string]any); ok && str(volume, "name") == "vault-renderer-token" {
				return fmt.Errorf("volume name vault-renderer-token already exists")
			}
		}
		token := map[string]any{"path": "token"}
		if settings["token-audience"] != "" {
			token["audience"] = settings["token-audience"]
		}
		if settings["token-expiration"] != "" {
			token["expirationSeconds"] = mustInt(settings["token-expiration"])
		}
		volumes = append(volumes, map[string]any{"name": "vault-renderer-token", "projected": map[string]any{"sources": []any{map[string]any{"serviceAccountToken": token}}}})
		ann[agent.AnnotationAgentServiceAccountTokenVolumeName] = "vault-renderer-token"
	}
	spec := map[string]any{}
	for key, value := range podSpec {
		spec[key] = value
	}
	spec["volumes"] = volumes
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": meta, "spec": spec}
	raw, err := json.Marshal(pod)
	if err != nil {
		return err
	}
	var typed corev1.Pod
	if err := json.Unmarshal(raw, &typed); err != nil {
		return fmt.Errorf("decode Pod for official injector: %w", err)
	}
	address := settings["service"]
	if address == "" {
		return fmt.Errorf("Vault address is required: set vault.hashicorp.com/service or an optional renderer vault-addr default")
	}
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Vault service must be an http or https URL without credentials, query, or fragment")
	}
	if namespace == "" {
		namespace = "default" // The injector requires a nonempty request namespace; this value is not emitted.
	}
	defaults := agent.AgentConfig{Address: address, AuthPath: "auth/kubernetes", Namespace: namespace, Image: agent.DefaultVaultImage, DefaultTemplate: "map", ResourceRequestCPU: "250m", ResourceRequestMem: "64Mi", ResourceLimitCPU: "500m", ResourceLimitMem: "128Mi", SetSecurityContext: true, ExitOnRetryFailure: true}
	if err := agent.Init(&typed, defaults); err != nil {
		return err
	}
	injected, err := agent.New(&typed)
	if err != nil {
		return err
	}
	if err := injected.Validate(); err != nil {
		return err
	}
	sort.Slice(injected.Secrets, func(i, j int) bool { return injected.Secrets[i].RawName < injected.Secrets[j].RawName })
	patchBytes, err := injected.Patch()
	if err != nil {
		return err
	}
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		return err
	}
	updated, err := patch.Apply(raw)
	if err != nil {
		return err
	}
	var output map[string]any
	if err := json.Unmarshal(updated, &output); err != nil {
		return err
	}
	if err := validateOfficialPod(object(output, "spec")); err != nil {
		return err
	}
	newMeta := object(output, "metadata")
	if _, exists := podMeta["name"]; !exists {
		delete(newMeta, "name")
	}
	if _, exists := podMeta["namespace"]; !exists {
		delete(newMeta, "namespace")
	}
	newAnnotations := ensureObject(newMeta, "annotations")
	hash := sha256.Sum256(patchBytes)
	newAnnotations[officialEngine] = hex.EncodeToString(hash[:])
	for key := range podMeta {
		delete(podMeta, key)
	}
	for key, value := range newMeta {
		podMeta[key] = value
	}
	for key := range podSpec {
		delete(podSpec, key)
	}
	for key, value := range object(output, "spec") {
		podSpec[key] = value
	}
	return nil
}

func validateOfficialPod(spec map[string]any) error {
	seenVolumes := map[string]bool{}
	for _, item := range array(spec, "volumes") {
		volume, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid volume")
		}
		name := str(volume, "name")
		if seenVolumes[name] {
			return fmt.Errorf("duplicate volume %q after official injection", name)
		}
		seenVolumes[name] = true
	}
	seenContainers := map[string]bool{}
	for _, kind := range []string{"initContainers", "containers"} {
		for _, item := range array(spec, kind) {
			container, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid container")
			}
			name := str(container, "name")
			if seenContainers[name] {
				return fmt.Errorf("duplicate container %q after official injection", name)
			}
			seenContainers[name] = true
			mounts := map[string]bool{}
			for _, entry := range array(container, "volumeMounts") {
				mount, ok := entry.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid volume mount")
				}
				path := str(mount, "mountPath")
				if mounts[path] {
					return fmt.Errorf("duplicate mount path %q in container %q", path, name)
				}
				mounts[path] = true
			}
		}
	}
	return nil
}
