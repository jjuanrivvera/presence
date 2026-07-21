package client

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// EnvFilePath is the shared config file read by the binary itself (hooks must
// not depend on the user's shell sourcing anything).
func EnvFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "presence", "env")
}

// ParseEnvFile reads KEY=VALUE lines; '#' starts a comment, blank lines are
// skipped. It is NOT a shell source — no expansion, no quoting semantics
// beyond trimming a single pair of surrounding quotes.
func ParseEnvFile(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// Resolve returns the value for key with precedence: flag > env var > env-file.
func Resolve(flagVal, key string, envFile map[string]string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(key); v != "" {
		return v
	}
	return envFile[key]
}

// StateDir is where the client persists its fallback session id.
func StateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "presence")
}
