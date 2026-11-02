package renderer

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"gopkg.in/yaml.v3"
)

const deployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: payment-api
  labels: {app: payment}
spec:
  template:
    metadata:
      annotations: {other: keep}
    spec:
      serviceAccountName: payment
      initContainers:
        - name: setup
          image: busybox
      containers:
        - name: web
          image: nginx
        - name: worker
          image: busybox
---
apiVersion: v1
kind: Service
metadata: {name: payment-api}
spec: {ports: [{port: 80}]}
`

const config = `vault-injector:
  enabled: true
  defaults:
    vault-addr: https://vault.example.com
    agent-image: hashicorp/vault:1.20
  targets:
    - kind: Deployment
      name: payment-api
      inject:
        role: payment
        agent-inject-secret-db: secret/data/payment/db
        agent-inject-file-db: db.env
        agent-inject-template-db: |
          {{- with secret "secret/data/payment/db" -}}
          PASSWORD={{ .Data.data.password }}
          {{- end }}
`

func TestRenderIsIdempotentAndPreservesUnrelatedResources(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	one, err := Render([]byte(deployment), cfg, "production")
	if err != nil {
		t.Fatal(err)
	}
	two, err := Render(one, cfg, "production")
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != string(two) {
		t.Fatal("second render changed output")
	}
	s := string(one)
	for _, expected := range []string{"kind: ConfigMap", "kind: Service", "name: setup", "name: web", "name: worker", "vault-agent-init", "vault-agent", "medium: Memory", "serviceAccountToken", "{{- with secret", "agent-inject-status: injected", "vault-renderer.dadianas.com/managed: \"true\""} {
		if !strings.Contains(s, expected) {
			t.Errorf("missing %q", expected)
		}
	}
}

func TestNoTargetsDoesNotInject(t *testing.T) {
	cfg, err := ParseConfig([]byte("vault-injector:\n  enabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "vault-agent") {
		t.Fatal("unexpected injection")
	}
}

func TestRejectsUnknownInjectSetting(t *testing.T) {
	_, err := ParseConfig([]byte(strings.Replace(config, "agent-inject-file-db", "agent-inject-unknown-db", 1)))
	if err == nil {
		t.Fatal("unknown setting accepted")
	}
}

func TestRejectsConflictingTargets(t *testing.T) {
	cfg, err := ParseConfig([]byte(config + "    - kind: Deployment\n      name: payment-api\n      inject:\n        role: other\n        agent-inject-secret-db: secret/data/other\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(deployment), cfg, ""); err == nil {
		t.Fatal("conflicting targets accepted")
	}
}

func TestCronJobNativeAnnotationsAndSelector(t *testing.T) {
	input := `apiVersion: batch/v1
kind: CronJob
metadata:
  name: nightly
  labels: {tier: batch}
spec:
  schedule: "0 0 * * *"
  jobTemplate:
    spec:
      template:
        metadata:
          annotations:
            vault.hashicorp.com/role: nightly
            vault.hashicorp.com/agent-inject-secret-key: secret/data/nightly
        spec:
          restartPolicy: Never
          containers: [{name: job, image: busybox}]
`
	cfg, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  defaults: {vault-addr: "https://vault.example.com"}
  targets:
    - kinds: [CronJob, Job]
      labelSelector:
        matchExpressions:
          - key: tier
            operator: In
            values: [batch]
      inject:
        annotations:
          vault.hashicorp.com/agent-pre-populate-only: "true"
`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	var docs []map[string]any
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		docs = append(docs, m)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs", len(docs))
	}
	s := string(out)
	if !strings.Contains(s, "vault-agent-init") || strings.Contains(s, "name: vault-agent\n") || !strings.Contains(s, "nightly-cronjob-vault-agent") {
		t.Fatal(s)
	}
}

func TestExclusionWinsAndMissingNamespaceIsNotDefault(t *testing.T) {
	cfg, err := ParseConfig([]byte(config + `  exclude:
    - namespace: production
`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "production")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("excluded workload injected")
	}
	out, err = Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("missing namespace treated as production")
	}
}

func TestSidecarOnlyDoesNotCreateInitContainer(t *testing.T) {
	cfg, err := ParseConfig([]byte(strings.Replace(config, "agent-inject-secret-db:", "agent-pre-populate: \"false\"\n        agent-inject-secret-db:", 1)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "name: vault-agent-init") || !strings.Contains(string(out), "name: vault-agent") {
		t.Fatal(string(out))
	}
}

func TestRejectsExistingOfficialInjectionArtifacts(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {other: keep, vault.hashicorp.com/agent-inject-status: injected}", 1)
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(input), cfg, ""); err == nil {
		t.Fatal("official-injected workload accepted")
	}
}

func TestRejectsForgedRendererMarker(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {other: keep, vault-renderer.dadianas.com/managed: \"true\"}", 1)
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(input), cfg, ""); err == nil {
		t.Fatal("marker without artifacts accepted")
	}
}

func TestUnselectedIncompleteWorkloadIsPreserved(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Render([]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: unrelated}\n"), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestDisabledConfigurationStillValidates(t *testing.T) {
	if _, err := ParseConfig([]byte("vault-injector:\n  enabled: false\n  defaults:\n    unsupported: bad\n")); err == nil {
		t.Fatal("invalid disabled config accepted")
	}
}

func TestRequiresConfigurationRoot(t *testing.T) {
	if _, err := ParseConfig([]byte("other: true\n")); err == nil {
		t.Fatal("missing root accepted")
	}
}

func TestDifferentKindsWithSameNameGetDistinctConfigMaps(t *testing.T) {
	cfg, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  defaults: {vault-addr: https://vault.example.com}
  targets:
    - kinds: [Deployment, StatefulSet]
      name: same
      inject: {role: same, agent-inject-secret-db: secret/data/same}
`))
	if err != nil {
		t.Fatal(err)
	}
	input := strings.ReplaceAll(strings.Split(deployment, "---")[0], "payment-api", "same")
	input += "---\n" + strings.Replace(input, "kind: Deployment", "kind: StatefulSet", 1)
	out, err := Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "kind: ConfigMap") != 2 {
		t.Fatal(string(out))
	}
}

func TestRejectsPlainHTTPVaultAddress(t *testing.T) {
	cfg, err := ParseConfig([]byte(strings.Replace(config, "https://vault.example.com", "http://vault.example.com", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(deployment), cfg, ""); err == nil {
		t.Fatal("insecure Vault address accepted")
	}
}

func TestGeneratedAgentConfigsParseAsHCL(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m["kind"] != "ConfigMap" {
			continue
		}
		for filename, raw := range m["data"].(map[string]any) {
			_, diags := hclsyntax.ParseConfig([]byte(raw.(string)), filename, hcl.Pos{Line: 1, Column: 1})
			if diags.HasErrors() {
				t.Fatalf("%s: %s", filename, diags.Error())
			}
		}
	}
}

func TestInjectedFilesAreReadableByPodGroup(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, part := range []string{"fsGroup: 1000", "runAsGroup: 1000", "runAsNonRoot: true", "runAsUser: 1000"} {
		if !strings.Contains(s, part) {
			t.Errorf("missing %s", part)
		}
	}
}

func TestExactTargetOverridesLabelTarget(t *testing.T) {
	cfg, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  defaults: {vault-addr: https://vault.example.com}
  targets:
    - kind: Deployment
      labelSelector: {matchLabels: {app: payment}}
      inject: {role: generic, agent-inject-secret-db: secret/data/generic}
    - kind: Deployment
      name: payment-api
      inject: {role: payment, agent-inject: "true", agent-inject-secret-db: secret/data/payment}
`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "secret/data/payment") || strings.Contains(string(out), "secret/data/generic") {
		t.Fatal(string(out))
	}
}

func TestTargetNeedsExplicitWorkloadSelector(t *testing.T) {
	_, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  targets:
    - kind: Deployment
      inject: {role: all, agent-inject-secret-db: secret/data/all}
`))
	if err == nil {
		t.Fatal("kind-only target accepted")
	}
}

func TestRejectsUnsafeDestination(t *testing.T) {
	cfg, err := ParseConfig([]byte(strings.Replace(config, "db.env", "/", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(deployment), cfg, ""); err == nil {
		t.Fatal("root destination accepted")
	}
}

func TestConfigMapPrecedesWorkload(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(out), "kind: ConfigMap") > strings.Index(string(out), "kind: Deployment") {
		t.Fatal("workload precedes generated ConfigMap")
	}
}

func TestDuplicateTemplateDestinationsFail(t *testing.T) {
	configWithDuplicate := strings.Replace(config, "agent-inject-file-db: db.env", "agent-inject-file-db: db.env\n        agent-inject-secret-other: secret/data/other\n        agent-inject-file-other: db.env", 1)
	cfg, err := ParseConfig([]byte(configWithDuplicate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(deployment), cfg, ""); err == nil {
		t.Fatal("duplicate destination accepted")
	}
}

func TestDisabledMatchingTargetWins(t *testing.T) {
	cfg, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  defaults: {vault-addr: https://vault.example.com}
  targets:
    - kind: Deployment
      name: payment-api
      inject: {role: payment, agent-inject: "true", agent-inject-secret-db: secret/data/payment}
    - kind: Deployment
      labelSelector: {matchLabels: {app: payment}}
      inject: {agent-inject: "false"}
`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("disabled target injected")
	}
}

func TestRejectsCredentialsInVaultURL(t *testing.T) {
	cfg, err := ParseConfig([]byte(strings.Replace(config, "https://vault.example.com", "https://token@vault.example.com", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(deployment), cfg, ""); err == nil {
		t.Fatal("URL credentials accepted")
	}
}

func TestRejectsEmptySecretIdentifier(t *testing.T) {
	cfg, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  defaults: {vault-addr: https://vault.example.com}
  targets:
    - kind: Deployment
      name: payment-api
      inject: {role: payment, agent-inject-secret-: secret/data/payment}
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(deployment), cfg, ""); err == nil {
		t.Fatal("empty secret identifier accepted")
	}
}

func TestCustomLabelsCannotOverrideOwnershipMarker(t *testing.T) {
	malicious := strings.Replace(config, "  targets:", "  labels:\n    vault-renderer.dadianas.com/managed: \"false\"\n  targets:", 1)
	if _, err := ParseConfig([]byte(malicious)); err == nil {
		t.Fatal("reserved marker label override accepted")
	}
}

func TestDottedWorkloadNameProducesValidConfigMapName(t *testing.T) {
	cfg, err := ParseConfig([]byte(strings.Replace(config, "payment-api", "payment.api", 2)))
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Replace(deployment, "name: payment-api", "name: payment.api", 1)
	out, err := Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "name: payment-api-deployment-vault-agent") {
		t.Fatal(string(out))
	}
}

func TestRejectsInvalidCustomLabel(t *testing.T) {
	bad := strings.Replace(config, "  targets:", "  labels:\n    invalid/key/part: value\n  targets:", 1)
	if _, err := ParseConfig([]byte(bad)); err == nil {
		t.Fatal("invalid Kubernetes label accepted")
	}
}

func TestInvalidManifestYAMLFails(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte("kind: Deployment\nmetadata: ["), cfg, ""); err == nil {
		t.Fatal("invalid YAML accepted")
	}
}

func TestVaultInitRunsBeforeExistingInitContainers(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Index(s, "name: vault-agent-init") > strings.Index(s, "name: setup") {
		t.Fatal("existing init runs before Vault renders secrets")
	}
}

func TestAnnotationDiscoveryRequiresExplicitOptIn(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {other: keep, vault.hashicorp.com/agent-inject: \"true\", vault.hashicorp.com/role: payment, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment}", 1)
	cfg, err := ParseConfig([]byte("vault-injector:\n  enabled: true\n  annotationDiscovery: true\n  defaults: {vault-addr: https://vault.example.com}\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("annotated workload not injected")
	}
	cfg, err = ParseConfig([]byte("vault-injector:\n  enabled: true\n  defaults: {vault-addr: https://vault.example.com}\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err = Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("annotation discovery was implicit")
	}
}

func TestGlobalModeRequiresExplicitOptInAndSkipsSystemNamespace(t *testing.T) {
	cfg, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  mode: all
  defaults:
    vault-addr: https://vault.example.com
    role: shared
    agent-inject-secret-db: secret/data/shared
`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "production")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("explicit global mode did not inject")
	}
	out, err = Render([]byte(deployment), cfg, "kube-system")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("system namespace injected globally")
	}
}

func TestAgentResourceAnnotationsApplyToInitAndSidecar(t *testing.T) {
	cfg, err := ParseConfig([]byte(strings.Replace(config, "    agent-image: hashicorp/vault:1.20", "    agent-image: hashicorp/vault:1.20\n    agent-requests-cpu: 100m\n    agent-limits-cpu: 300m\n    agent-requests-mem: 32Mi\n    agent-limits-mem: 96Mi", 1)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, value := range []string{"cpu: 100m", "cpu: 300m", "memory: 32Mi", "memory: 96Mi"} {
		if strings.Count(s, value) != 2 {
			t.Errorf("expected both Agent containers to have %s", value)
		}
	}
}

func TestInvalidAgentResourcesFailValidation(t *testing.T) {
	bad := strings.Replace(config, "    agent-image: hashicorp/vault:1.20", "    agent-image: hashicorp/vault:1.20\n    agent-limits-cpu: nonsense", 1)
	if _, err := ParseConfig([]byte(bad)); err == nil || !strings.Contains(err.Error(), "quantity") {
		t.Fatalf("wanted invalid quantity error, got %v", err)
	}
}

func TestExplicitHTTPOptInForLocalVault(t *testing.T) {
	local := strings.Replace(config, "https://vault.example.com", "http://vault.vault-renderer-test.svc:8200", 1)
	local = strings.Replace(local, "    agent-image: hashicorp/vault:1.20", "    agent-image: hashicorp/vault:1.20\n    allow-http: \"true\"", 1)
	cfg, err := ParseConfig([]byte(local))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render([]byte(deployment), cfg, ""); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateRefreshIntervalAppearsInAgentConfig(t *testing.T) {
	configured := strings.Replace(config, "    agent-image: hashicorp/vault:1.20", "    agent-image: hashicorp/vault:1.20\n    template-static-secret-render-interval: 10s", 1)
	cfg, err := ParseConfig([]byte(configured))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "static_secret_render_interval = \"10s\"") != 2 {
		t.Fatal("refresh interval missing from Agent configs")
	}
}

func TestAgentBypassesRootOnlyImageEntrypoint(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "- /bin/vault") != 2 {
		t.Fatal("Agent containers must invoke the Vault binary directly")
	}
}

func TestRejectsCAFileWithoutTLSSecret(t *testing.T) {
	input := strings.Replace(config, "    agent-image: hashicorp/vault:1.20", "    agent-image: hashicorp/vault:1.20\n    tls-ca-cert: ca.pem", 1)
	if _, err := ParseConfig([]byte(input)); err == nil {
		t.Fatal("CA filename without a Kubernetes Secret was accepted")
	}
}

func TestRequiresExplicitEnabledSetting(t *testing.T) {
	input := strings.Replace(config, "  enabled: true\n", "", 1)
	if _, err := ParseConfig([]byte(input)); err == nil {
		t.Fatal("configuration with targets but no enabled setting was accepted")
	}
}
