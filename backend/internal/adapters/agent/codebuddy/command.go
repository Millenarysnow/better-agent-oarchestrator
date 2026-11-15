package codebuddy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// commandPrefix bypasses the official npm Windows batch shim. Go cannot start
// a .cmd directly, and routing settings JSON through cmd.exe loses argv safety.
// Native executables and Unix launchers keep their normal execution path.
func commandPrefix(binary string) ([]string, error) {
	ext := strings.ToLower(filepath.Ext(binary))
	if ext != ".cmd" && ext != ".bat" {
		return []string{binary}, nil
	}
	dir := filepath.Dir(binary)
	entry := filepath.Join(dir, "node_modules", "@tencent-ai", "codebuddy-code", "bin", "codebuddy")
	shim, err := os.ReadFile(binary) //nolint:gosec // user-selected executable
	if err != nil {
		return nil, fmt.Errorf("read CodeBuddy npm shim: %w", err)
	}
	// Only unwrap the documented package's npm launcher, never silently skip a
	// custom wrapper that could select a different CLI, environment, or account.
	normalized := strings.ReplaceAll(strings.ToLower(string(shim)), `\`, "/")
	if !strings.Contains(normalized, "/node_modules/@tencent-ai/codebuddy-code/bin/codebuddy") || !regularFile(entry) {
		return nil, fmt.Errorf("CodeBuddy command shim %q is not the official npm layout; reinstall @tencent-ai/codebuddy-code or use the native codebuddy.exe", binary)
	}
	node := filepath.Join(dir, "node.exe")
	if !regularFile(node) {
		node, err = exec.LookPath("node")
		if err != nil {
			return nil, fmt.Errorf("CodeBuddy npm installation requires Node.js on PATH: %w", err)
		}
	}
	return []string{node, entry}, nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
