package renderer

import (
	"encoding/base64"
	"encoding/json"
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

func TestEmptyOptionalConfigurationEnablesAnnotationDiscovery(t *testing.T) {
	cfg, err := ParseConfig([]byte("vault-injector: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Injector.Enabled || !cfg.Injector.AnnotationDiscovery {
		t.Fatal("annotation discovery was not the default")
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

func TestDuplicateOrdinaryManifestKeysUseLastValue(t *testing.T) {
	input := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: reports
  labels:
    appcode: first
    appcode: final
spec:
  template:
    metadata:
      labels:
        opscontact: first
        opscontact: final
      annotations:
        vault.hashicorp.com/agent-inject: "true"
        vault.hashicorp.com/service: https://vault.example.com
        vault.hashicorp.com/role: reports
        vault.hashicorp.com/agent-inject-secret-config: secret/data/reports
    spec:
      containers: [{name: app, image: nginx}]
`
	out, err := Render([]byte(input), DefaultConfig(), "")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "name: vault-agent-init") || !strings.Contains(s, "appcode: final") || !strings.Contains(s, "opscontact: final") || strings.Contains(s, "appcode: first") || strings.Contains(s, "opscontact: first") {
		t.Fatal(s)
	}
}

func TestDuplicateVaultAnnotationFailsClearly(t *testing.T) {
	input := `apiVersion: v1
kind: Pod
metadata:
  name: demo
  annotations:
    vault.hashicorp.com/agent-inject: "true"
    vault.hashicorp.com/agent-inject: "false"
spec:
  containers: [{name: app, image: nginx}]
`
	_, err := Render([]byte(input), DefaultConfig(), "")
	if err == nil || !strings.Contains(err.Error(), "duplicate Vault annotation") {
		t.Fatal("duplicate Vault annotation did not fail clearly", err)
	}
}

func TestOptionalConfigStillRejectsDuplicateKeys(t *testing.T) {
	_, err := ParseConfig([]byte("vault-injector:\n  enabled: true\n  enabled: false\n"))
	if err == nil {
		t.Fatal("duplicate renderer configuration key was accepted")
	}
}

func TestVaultInitFollowsExistingInitContainersUnlessRequestedFirst(t *testing.T) {
	cfg, err := ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(deployment), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Index(s, "name: vault-agent-init") < strings.Index(s, "name: setup") {
		t.Fatal("Vault init ran first without agent-init-first")
	}
	first, err := ParseConfig([]byte(strings.Replace(config, "    agent-image: hashicorp/vault:1.20", "    agent-image: hashicorp/vault:1.20\n    agent-init-first: \"true\"", 1)))
	if err != nil {
		t.Fatal(err)
	}
	out, err = Render([]byte(deployment), first, "")
	if err != nil {
		t.Fatal(err)
	}
	s = string(out)
	if strings.Index(s, "name: vault-agent-init") > strings.Index(s, "name: setup") {
		t.Fatal("agent-init-first did not put Vault first")
	}
}

func TestDefaultTemplateMatchesOfficialMapAndAllowsJSON(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {vault.hashicorp.com/agent-inject: \"true\", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: payment, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment}", 1)
	out, err := Render([]byte(input), DefaultConfig(), "")
	if err != nil {
		t.Fatal(err)
	}
	contents := agentConfigFromRendered(t, out, "vault-agent")["template"].([]any)[0].(map[string]any)["contents"].(string)
	if !strings.Contains(contents, "range $k, $v := .Data") || strings.Contains(contents, "toJSON") {
		t.Fatal("default template did not use the official map form")
	}
	input = strings.Replace(input, "vault.hashicorp.com/agent-inject-secret-db:", "vault.hashicorp.com/agent-inject-default-template: json, vault.hashicorp.com/agent-inject-secret-db:", 1)
	out, err = Render([]byte(input), DefaultConfig(), "")
	if err != nil {
		t.Fatal(err)
	}
	contents = agentConfigFromRendered(t, out, "vault-agent")["template"].([]any)[0].(map[string]any)["contents"].(string)
	if !strings.Contains(contents, "toJSON") {
		t.Fatal("json default template was not honored", err)
	}
}

func TestAnnotationDiscoveryDefaultsOnAndCanBeDisabled(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {other: keep, vault.hashicorp.com/agent-inject: \"true\", vault.hashicorp.com/role: payment, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment}", 1)
	cfg, err := ParseConfig([]byte("vault-injector:\n  enabled: true\n  defaults: {vault-addr: https://vault.example.com}\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "name: vault-agent-init") {
		t.Fatal("annotated workload not injected")
	}
	cfg, err = ParseConfig([]byte("vault-injector:\n  enabled: true\n  annotationDiscovery: false\n  defaults: {vault-addr: https://vault.example.com}\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err = Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "name: vault-agent-init") {
		t.Fatal("annotation discovery opt-out was ignored")
	}
	input = strings.Replace(input, `vault.hashicorp.com/agent-inject: "true"`, `vault.hashicorp.com/agent-inject: "false"`, 1)
	cfg, err = ParseConfig([]byte("vault-injector:\n  enabled: true\n  defaults: {vault-addr: https://vault.example.com}\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err = Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "name: vault-agent-init") {
		t.Fatal("explicit false annotation was injected without a target")
	}
}

func TestDiscoveredUnsupportedAnnotationFails(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {vault.hashicorp.com/agent-inject: \"true\", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: payment, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment, vault.hashicorp.com/does-not-exist: value}", 1)
	_, err := Render([]byte(input), DefaultConfig(), "")
	if err == nil || !strings.Contains(err.Error(), "unsupported Vault annotations") {
		t.Fatal("unsupported injector annotation did not fail clearly", err)
	}
}

func TestNativeVaultCAAndTLSAnnotations(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", `annotations: {vault.hashicorp.com/agent-inject: "true", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: payment, vault.hashicorp.com/namespace: engineering/dev, vault.hashicorp.com/tls-secret: company-ca, vault.hashicorp.com/ca-cert: /vault/tls/ca.pem, vault.hashicorp.com/tls-server-name: vault.example.com, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment}`, 1)
	out, err := Render([]byte(input), DefaultConfig(), "production")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`secretName: company-ca`, `mountPath: /vault/tls`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in rendered output", want)
		}
	}
	agentConfig := agentConfigFromRendered(t, out, "vault-agent")
	vault := agentConfig["vault"].(map[string]any)
	if vault["ca_cert"] != "/vault/tls/ca.pem" || vault["tls_server_name"] != "vault.example.com" {
		t.Fatal("CA and TLS server name were not passed to Vault Agent")
	}
}

func TestOfficialAnnotationsDrivePreDeploymentMutation(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", `annotations: {vault.hashicorp.com/agent-inject: "true", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: payment, vault.hashicorp.com/namespace: engineering/dev, vault.hashicorp.com/tls-secret: company-ca, vault.hashicorp.com/ca-cert: /vault/tls/ca.pem, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment, vault.hashicorp.com/agent-inject-command-db: reload, vault.hashicorp.com/agent-cache-enable: "true", vault.hashicorp.com/agent-inject-token: "true"}`, 1)
	out, err := Render([]byte(input), DefaultConfig(), "production")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("official annotation path generated an unnecessary ConfigMap")
	}
	agentConfig := agentConfigFromRendered(t, out, "vault-agent")
	auth := agentConfig["auto_auth"].(map[string]any)["method"].(map[string]any)
	vault := agentConfig["vault"].(map[string]any)
	template := agentConfig["template"].([]any)[0].(map[string]any)
	if auth["namespace"] != "engineering/dev" || vault["ca_cert"] != "/vault/tls/ca.pem" || template["command"] != "reload" || agentConfig["cache"] == nil {
		t.Fatalf("official Agent settings were not preserved: %#v", agentConfig)
	}
	second, err := Render(out, DefaultConfig(), "production")
	if err != nil || string(out) != string(second) {
		t.Fatal("official annotation render is not idempotent", err)
	}
}

func TestOfficialAnnotationOutputIsDeterministicWithSeveralSecrets(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", `annotations: {vault.hashicorp.com/agent-inject: "true", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: payment, vault.hashicorp.com/agent-inject-secret-zeta: secret/data/zeta, vault.hashicorp.com/agent-inject-secret-alpha: secret/data/alpha}`, 1)
	var first string
	for i := 0; i < 20; i++ {
		out, err := Render([]byte(input), DefaultConfig(), "production")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(out)
		} else if string(out) != first {
			t.Fatal("official injection output changed across identical renders")
		}
	}
}

func TestOfficialAnnotationStartupModes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        string
		wantInit    bool
		wantSidecar bool
	}{
		{name: "default", wantInit: true, wantSidecar: true},
		{name: "init only", mode: `vault.hashicorp.com/agent-pre-populate-only: "true", `, wantInit: true},
		{name: "sidecar only", mode: `vault.hashicorp.com/agent-pre-populate: "false", `, wantSidecar: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(deployment, "annotations: {other: keep}", `annotations: {vault.hashicorp.com/agent-inject: "true", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: payment, `+tc.mode+`vault.hashicorp.com/agent-inject-secret-db: secret/data/payment}`, 1)
			out, err := Render([]byte(input), DefaultConfig(), "production")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "name: vault-agent-init") != tc.wantInit || strings.Contains(string(out), "name: vault-agent\n") != tc.wantSidecar {
				t.Fatal("wrong Agent container pattern")
			}
		})
	}
}

func TestOfficialAnnotationDiscoveryAcrossWorkloadKinds(t *testing.T) {
	annotations := map[string]any{
		prefix + "agent-inject": "true", prefix + "service": "https://vault.example.com",
		prefix + "role": "demo", prefix + "agent-inject-secret-config": "secret/data/demo",
	}
	for _, tc := range []struct{ kind, apiVersion string }{
		{"Deployment", "apps/v1"}, {"StatefulSet", "apps/v1"}, {"DaemonSet", "apps/v1"},
		{"Job", "batch/v1"}, {"CronJob", "batch/v1"}, {"Pod", "v1"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			pod := map[string]any{"metadata": map[string]any{"annotations": annotations}, "spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": "nginx"}}}}
			resource := map[string]any{"apiVersion": tc.apiVersion, "kind": tc.kind, "metadata": map[string]any{"name": "demo"}}
			switch tc.kind {
			case "Pod":
				resource["metadata"].(map[string]any)["annotations"] = annotations
				resource["spec"] = pod["spec"]
			case "CronJob":
				resource["spec"] = map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": pod}}}
			default:
				resource["spec"] = map[string]any{"template": pod}
			}
			input, err := yaml.Marshal(resource)
			if err != nil {
				t.Fatal(err)
			}
			out, err := Render(input, DefaultConfig(), "apps")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(out), "name: vault-agent-init") != 1 || strings.Count(string(out), "name: vault-agent\n") != 1 {
				t.Fatal("workload was not injected exactly once")
			}
		})
	}
}

func agentConfigFromRendered(t *testing.T, output []byte, name string) map[string]any {
	t.Helper()
	var manifest map[string]any
	if err := yaml.Unmarshal(output, &manifest); err != nil {
		t.Fatal(err)
	}
	podMeta, pod, err := podParts(manifest)
	if err != nil || podMeta == nil {
		t.Fatal("rendered workload lacks PodSpec", err)
	}
	for _, kind := range []string{"initContainers", "containers"} {
		for _, item := range array(pod, kind) {
			container := item.(map[string]any)
			if str(container, "name") != name {
				continue
			}
			for _, entry := range array(container, "env") {
				env := entry.(map[string]any)
				if str(env, "name") != "VAULT_CONFIG" {
					continue
				}
				decoded, err := base64.StdEncoding.DecodeString(str(env, "value"))
				if err != nil {
					t.Fatal(err)
				}
				var config map[string]any
				if err := json.Unmarshal(decoded, &config); err != nil {
					t.Fatal(err)
				}
				return config
			}
		}
	}
	t.Fatalf("%s has no VAULT_CONFIG", name)
	return nil
}

func TestReportsAllUnsupportedAnnotationsTogether(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", `annotations: {vault.hashicorp.com/agent-inject: "true", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: payment, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment, vault.hashicorp.com/agent-unknown-one: "true", vault.hashicorp.com/agent-unknown-two: reload, vault.hashicorp.com/unknown-proxy: https://proxy.example.com}`, 1)
	_, err := Render([]byte(input), DefaultConfig(), "")
	if err == nil {
		t.Fatal("unsupported annotations were accepted")
	}
	message := err.Error()
	previous := -1
	for _, key := range []string{"agent-unknown-one", "agent-unknown-two", "unknown-proxy"} {
		position := strings.Index(message, key)
		if position <= previous {
			t.Fatalf("unsupported settings were not all reported in sorted order: %s", message)
		}
		previous = position
	}
}

func TestVaultEnterpriseNamespaceFromAnnotationAndTargetOverride(t *testing.T) {
	input := strings.Replace(deployment, "payment-api", "webhook-notif", 1)
	input = strings.Replace(input, "annotations: {other: keep}", `annotations: {vault.hashicorp.com/agent-inject: "true", vault.hashicorp.com/service: https://vault.example.com, vault.hashicorp.com/role: webhook-notif, vault.hashicorp.com/namespace: engineering/dev, vault.hashicorp.com/agent-inject-secret-config: secret/data/webhook-notif}`, 1)
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "native annotation", cfg: DefaultConfig(), want: "engineering/dev"},
		{name: "annotation beats default alias", cfg: mustParseConfig(t, `vault-injector:
  defaults: {vault-namespace: engineering/default}
`), want: "engineering/dev"},
		{name: "legacy target alias", cfg: mustParseConfig(t, `vault-injector:
  targets:
    - kind: Deployment
      name: webhook-notif
      inject: {vault-namespace: engineering/prod}
`), want: "engineering/prod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Render([]byte(input), tc.cfg, "production")
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]any
			if err := yaml.Unmarshal(out, &manifest); err != nil {
				t.Fatal(err)
			}
			if object(object(object(manifest, "spec"), "template"), "metadata") == nil {
				t.Fatal("missing Pod template")
			}
			for _, container := range []string{"vault-agent-init", "vault-agent"} {
				config := agentConfigFromRendered(t, out, container)
				method := config["auto_auth"].(map[string]any)["method"].(map[string]any)
				if method["namespace"] != tc.want {
					t.Fatalf("%s namespace = %v, want %s", container, method["namespace"], tc.want)
				}
			}
		})
	}
}

func mustParseConfig(t *testing.T, input string) Config {
	t.Helper()
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDiscoveredAnnotationWithoutVaultAddressExplainsMissingDefault(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {vault.hashicorp.com/agent-inject: \"true\", vault.hashicorp.com/role: payment, vault.hashicorp.com/agent-inject-secret-db: secret/data/payment}", 1)
	_, err := Render([]byte(input), DefaultConfig(), "")
	if err == nil || !strings.Contains(err.Error(), "vault.hashicorp.com/service or an optional renderer vault-addr default") {
		t.Fatal("missing Vault address was not explained", err)
	}
}

func TestEnvironmentTargetOverridesRenderedAnnotations(t *testing.T) {
	input := strings.Replace(deployment, "annotations: {other: keep}", "annotations: {other: keep, vault.hashicorp.com/agent-inject: \"false\", vault.hashicorp.com/role: chart-role, vault.hashicorp.com/agent-inject-secret-db: secret/data/chart}", 1)
	cfg, err := ParseConfig([]byte(`vault-injector:
  enabled: true
  defaults: {vault-addr: https://vault.example.com}
  targets:
    - kind: Deployment
      name: payment-api
      inject:
        agent-inject: "true"
        role: env-role
        agent-inject-secret-db: secret/data/env
`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render([]byte(input), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "kind: ConfigMap") {
		t.Fatal("annotated workload used the target-only engine")
	}
	agentConfig := agentConfigFromRendered(t, out, "vault-agent")
	auth := agentConfig["auto_auth"].(map[string]any)["method"].(map[string]any)
	role := auth["config"].(map[string]any)["role"]
	secret := agentConfig["template"].([]any)[0].(map[string]any)["contents"].(string)
	if role != "env-role" || !strings.Contains(secret, "secret/data/env") || strings.Contains(secret, "secret/data/chart") {
		t.Fatal("environment override did not win")
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

func TestEnabledSettingDefaultsOn(t *testing.T) {
	input := strings.Replace(config, "  enabled: true\n", "", 1)
	cfg, err := ParseConfig([]byte(input))
	if err != nil || !cfg.Injector.Enabled {
		t.Fatal("configuration without enabled did not default on", err)
	}
}
