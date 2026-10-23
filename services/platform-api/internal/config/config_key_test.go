package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSecretKey(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secret-key")
	if err := os.WriteFile(file, []byte("  a2V5LWZyb20tdHBt  \n"), 0o400); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PLATFORM_SECRET_KEY", "")
	t.Setenv("PLATFORM_SECRET_KEY_FILE", file)
	if key, err := loadSecretKey(); err != nil || key != "a2V5LWZyb20tdHBt" {
		t.Fatalf("the unsealed file should provide the key, trimmed: %q, %v", key, err)
	}

	t.Setenv("PLATFORM_SECRET_KEY", "inline")
	if _, err := loadSecretKey(); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both at once must be refused, got %v", err)
	}

	t.Setenv("PLATFORM_SECRET_KEY_FILE", "")
	if key, err := loadSecretKey(); err != nil || key != "inline" {
		t.Fatalf("inline still works: %q, %v", key, err)
	}

	t.Setenv("PLATFORM_SECRET_KEY", "")
	t.Setenv("PLATFORM_SECRET_KEY_FILE", filepath.Join(dir, "not-unsealed"))
	if _, err := loadSecretKey(); err == nil || !strings.Contains(err.Error(), "unsealed") {
		t.Fatalf("a missing file must say the key isn't unsealed, got %v", err)
	}

	t.Setenv("PLATFORM_SECRET_KEY_FILE", "")
	if _, err := loadSecretKey(); err == nil {
		t.Fatal("no key at all must be refused")
	}
}
