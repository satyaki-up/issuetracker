package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsConfigInParentAndResolvesRelativeDBPath(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "repo")
	subDir := filepath.Join(projectDir, "a", "b")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfgContent := "db=.it/issues.db\nproject=c4t\n"
	if err := os.WriteFile(filepath.Join(projectDir, FileName), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Discover(subDir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}

	wantDB := filepath.Join(projectDir, ".it", "issues.db")
	if cfg.DBPath != wantDB {
		t.Fatalf("expected db path %q, got %q", wantDB, cfg.DBPath)
	}
	if cfg.Project != "c4t" {
		t.Fatalf("expected project c4t, got %q", cfg.Project)
	}
}

func TestDiscoverNoConfigReturnsNil(t *testing.T) {
	cfg, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config, got %+v", cfg)
	}
}

func TestDiscoverRejectsInvalidProjectPrefix(t *testing.T) {
	dir := t.TempDir()
	content := "project=toolong\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Discover(dir)
	if err == nil {
		t.Fatal("expected error for invalid project")
	}
}
