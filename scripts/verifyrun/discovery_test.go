package verifyrun

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifierDiscoversEveryRegisteredFeature(t *testing.T) {
	var output bytes.Buffer
	if code := Run(t.TempDir(), []string{"--help"}, &output); code != 0 {
		t.Fatal("help exit", code)
	}
	index, err := os.ReadFile(filepath.Join("..", "..", "verify", "features", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range featureJourneys {
		if !strings.Contains(output.String(), item.name) {
			t.Errorf("help omits registered feature %s", item.name)
		}
		if !bytes.Contains(index, []byte("--feature "+item.name+"`")) {
			t.Errorf("feature index omits registered feature %s", item.name)
		}
	}
}
