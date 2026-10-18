package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The variable wins; without it the file it names, trimmed; neither is "".
func TestSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(path, []byte("  postgresql://x\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIKAN_TEST_SECRET", "")
	t.Setenv("MIKAN_TEST_SECRET_FILE", path)
	if v, err := Secret("MIKAN_TEST_SECRET"); err != nil || v != "postgresql://x" {
		t.Fatalf("file: %q %v", v, err)
	}
	t.Setenv("MIKAN_TEST_SECRET", "direct")
	if v, _ := Secret("MIKAN_TEST_SECRET"); v != "direct" {
		t.Fatalf("variable: %q", v)
	}
	t.Setenv("MIKAN_TEST_SECRET", "")
	t.Setenv("MIKAN_TEST_SECRET_FILE", "")
	if v, err := Secret("MIKAN_TEST_SECRET"); v != "" || err != nil {
		t.Fatalf("none: %q %v", v, err)
	}
	t.Setenv("MIKAN_TEST_SECRET_FILE", filepath.Join(t.TempDir(), "missing"))
	if _, err := Secret("MIKAN_TEST_SECRET"); err == nil || !strings.Contains(err.Error(), "MIKAN_TEST_SECRET_FILE") {
		t.Fatalf("missing file: %v", err)
	}
}
