package renderer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedMultiWorkloadExample(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "multi-workload")
	config, err := os.ReadFile(filepath.Join(dir, "targets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := os.ReadFile(filepath.Join(dir, "manifests.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "rendered.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(manifests, cfg, "production")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("published rendered.yaml differs from current renderer output")
	}
}
