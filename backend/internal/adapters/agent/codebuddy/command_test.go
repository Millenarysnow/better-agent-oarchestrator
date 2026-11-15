package codebuddy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestWindowsNPMCommandPreservesLaunchAndRestoreArguments(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is not installed")
	}
	prefix := filepath.Join(t.TempDir(), "npm with spaces")
	entry := filepath.Join(prefix, "node_modules", "@tencent-ai", "codebuddy-code", "bin", "codebuddy")
	if err := os.MkdirAll(filepath.Dir(entry), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(`process.stdout.write(JSON.stringify(process.argv.slice(2)))`), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(prefix, "codebuddy.cmd")
	if err := os.WriteFile(shim, []byte(`@echo off
"%_prog%" "%dp0%\node_modules\@tencent-ai\codebuddy-code\bin\codebuddy" %*
`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{resolvedBinary: shim}
	workspace := filepath.Join(t.TempDir(), "workspace with spaces & symbols")
	cfg := ports.AgentConfig{Model: `model with "quotes" & symbols`}
	launch, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: workspace, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	restore, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"}},
		Config:  cfg,
	})
	if err != nil || !ok {
		t.Fatalf("restore=%v, err=%v", ok, err)
	}
	for _, args := range [][]string{launch, restore} {
		if args[1] != entry {
			t.Fatalf("command=%v, want direct Node entry", args)
		}
		out, err := exec.CommandContext(t.Context(), args[0], args[1:]...).Output()
		if err != nil {
			t.Fatal(err)
		}
		var received []string
		if err := json.Unmarshal(out, &received); err != nil || !reflect.DeepEqual(received, args[2:]) {
			t.Fatalf("received=%s, err=%v, want %q", out, err, args[2:])
		}
	}
}

func TestWindowsNPMCommandRejectsUnknownOrBrokenShim(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "codebuddy.cmd")
	if err := os.WriteFile(shim, []byte("@echo custom wrapper"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := commandPrefix(shim); err == nil {
		t.Fatal("unknown shim was silently bypassed")
	}
	if err := os.WriteFile(shim, []byte(`"%dp0%\node_modules\@tencent-ai\codebuddy-code\bin\codebuddy" %*`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := commandPrefix(shim); err == nil {
		t.Fatal("missing package entry was accepted")
	}
}
