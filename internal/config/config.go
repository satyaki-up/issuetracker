package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const FileName = "itconfig"

var projectPrefixRe = regexp.MustCompile(`^[a-z0-9]{3}$`)

type Config struct {
	Path    string
	DBPath  string
	Project string
}

func Discover(startDir string) (*Config, error) {
	dir := startDir
	for {
		candidate := filepath.Join(dir, FileName)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			cfg, err := parseFile(candidate)
			if err != nil {
				return nil, err
			}
			return cfg, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", candidate, err)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

func parseFile(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{Path: path}
	lines := strings.Split(string(content), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid %s:%d: expected key=value", path, i+1)
		}
		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)

		switch key {
		case "db":
			if value == "" {
				return nil, fmt.Errorf("invalid %s:%d: db cannot be empty", path, i+1)
			}
			if filepath.IsAbs(value) {
				cfg.DBPath = value
			} else {
				cfg.DBPath = filepath.Clean(filepath.Join(filepath.Dir(path), value))
			}
		case "project":
			prefix := strings.ToLower(value)
			if !projectPrefixRe.MatchString(prefix) {
				return nil, fmt.Errorf("invalid %s:%d: project must be 3 lowercase alphanumeric chars", path, i+1)
			}
			cfg.Project = prefix
		default:
			return nil, fmt.Errorf("invalid %s:%d: unsupported key %q", path, i+1, key)
		}
	}
	return cfg, nil
}
