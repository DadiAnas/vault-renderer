package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCommandRendersFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "vault.yaml")
	if err := os.WriteFile(file, []byte("replicaCount: 2\nvault-injector:\n  enabled: true\n  targets:\n    - kind: Pod\n      name: demo\n      inject:\n        role: demo\n        vault-addr: https://vault.example.com\n        agent-inject-secret-db: secret/data/demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAULT_RENDERER_CONFIG", file)
	in := strings.NewReader("apiVersion: v1\nkind: Pod\nmetadata: {name: demo}\nspec:\n  containers: [{name: app, image: nginx}]\n")
	var out, diag bytes.Buffer
	if code := run(nil, in, &out, &diag); code != 0 {
		t.Fatalf("exit %d: %s", code, diag.String())
	}
	if !strings.Contains(out.String(), "kind: ConfigMap") || diag.Len() != 0 {
		t.Fatalf("stdout=%s stderr=%s", out.String(), diag.String())
	}
}

func TestDefaultCommandDiscoversAnnotationsWithoutConfig(t *testing.T) {
	t.Setenv("VAULT_RENDERER_CONFIG", "")
	in := strings.NewReader(`apiVersion: v1
kind: Pod
metadata:
  name: demo
  annotations:
    vault.hashicorp.com/agent-inject: "true"
    vault.hashicorp.com/service: https://vault.example.com
    vault.hashicorp.com/role: demo
    vault.hashicorp.com/agent-inject-secret-db: secret/data/demo
spec:
  containers: [{name: app, image: nginx}]
`)
	var out, diag bytes.Buffer
	if code := run(nil, in, &out, &diag); code != 0 {
		t.Fatalf("exit %d: %s", code, diag.String())
	}
	if !strings.Contains(out.String(), "name: vault-agent-init") || !strings.Contains(out.String(), "agent-inject-status: injected") || diag.Len() != 0 {
		t.Fatalf("stdout=%s stderr=%s", out.String(), diag.String())
	}
	var second bytes.Buffer
	if code := run(nil, strings.NewReader(out.String()), &second, &diag); code != 0 || second.String() != out.String() {
		t.Fatalf("second render changed output: code=%d stderr=%s", code, diag.String())
	}
}

func TestValidateRejectsUnknownField(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(file, []byte("vault-injector:\n  enabled: true\n  surprise: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"validate", "--config", file}, strings.NewReader(""), &out, &diag); code == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "surprise") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
}

func TestHelm2ChartPackagesRenderedManifestsWithoutReevaluatingVaultTemplates(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "vault.yaml")
	output := filepath.Join(dir, "rendered")
	if err := os.WriteFile(configFile, []byte("vault-injector:\n  enabled: true\n  targets:\n    - kind: Pod\n      name: demo\n      inject:\n        role: demo\n        vault-addr: https://vault.example.com\n        agent-inject-secret-db: secret/data/demo\n        agent-inject-template-db: '{{ with secret \"secret/data/demo\" }}{{ .Data.data.password }}{{ end }}'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader("apiVersion: v1\nkind: Pod\nmetadata: {name: demo}\nspec:\n  containers: [{name: app, image: nginx}]\n")
	var out, diag bytes.Buffer
	if code := run([]string{"helm2-chart", "--config", configFile, "--output-dir", output}, in, &out, &diag); code != 0 {
		t.Fatalf("exit %d: %s", code, diag.String())
	}
	if out.Len() != 0 {
		t.Fatal("chart command wrote manifests to stdout")
	}
	files, err := os.ReadFile(filepath.Join(output, "files", "rendered.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files), "{{ with secret") {
		t.Fatal("Vault template was evaluated")
	}
	tmpl, err := os.ReadFile(filepath.Join(output, "templates", "rendered.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(tmpl) != "{{ .Files.Get \"files/rendered.yaml\" }}\n" {
		t.Fatal("rendered chart would re-evaluate Vault templates")
	}
	if _, err := os.Stat(filepath.Join(output, "Chart.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "files", "templates"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Errorf("%s mode = %04o, want 0700", name, info.Mode().Perm())
		}
	}
	for _, name := range []string{"Chart.yaml", "files/rendered.yaml", "templates/rendered.yaml"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %04o, want 0600", name, info.Mode().Perm())
		}
	}
}
