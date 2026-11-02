package renderer

import (
	"os"
	"strings"
	"testing"
)

func TestPinnedOfficialAnnotationInventory(t *testing.T) {
	data, err := os.ReadFile("testdata/vault-k8s-1.7.6-annotations.txt")
	if err != nil {
		t.Fatal(err)
	}
	keys := strings.Fields(string(data))
	if len(keys) != 74 {
		t.Fatalf("pinned inventory has %d keys, want 74", len(keys))
	}
	for _, key := range keys {
		if officialExact[key] {
			continue
		}
		if !isOfficialAnnotation(key + "-example") {
			t.Errorf("pinned official annotation %q is not recognized", key)
		}
	}
	if isOfficialAnnotation("agent-future-unknown") {
		t.Fatal("future annotation was silently accepted")
	}
}
