package codebuddy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLaunchUsesDocumentedPermissionModesAndAfterStartPrompt(t *testing.T) {
	for _, tc := range []struct {
		mode ports.PermissionMode
		want []string
	}{
		{ports.PermissionModeDefault, []string{"codebuddy", "--model", "test-model"}},
		{ports.PermissionModeAcceptEdits, []string{"codebuddy", "--permission-mode", "acceptEdits", "--model", "test-model"}},
		{ports.PermissionModeAuto, []string{"codebuddy", "--permission-mode", "auto", "--model", "test-model"}},
		{ports.PermissionModeBypassPermissions, []string{"codebuddy", "--permission-mode", "bypassPermissions", "--model", "test-model"}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			p := &Plugin{resolvedBinary: "codebuddy"}
			cfg := ports.LaunchConfig{
				Permissions:  tc.mode,
				Config:       ports.AgentConfig{Model: " test-model "},
				Prompt:       "--a task that starts with a dash",
				SystemPrompt: strings.Repeat("standing instructions ", 10000),
			}
			got, err := p.GetLaunchCommand(context.Background(), cfg)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("command = %v, err=%v, want %v", got, err, tc.want)
			}
			strategy, err := p.GetPromptDeliveryStrategy(context.Background(), cfg)
			if err != nil || strategy != ports.PromptDeliveryAfterStart {
				t.Fatalf("strategy = %q, err=%v", strategy, err)
			}
		})
	}
}

func TestRestoreUsesOnlyCapturedNativeSessionID(t *testing.T) {
	p := &Plugin{resolvedBinary: "codebuddy"}
	cfg := ports.RestoreConfig{
		Session: ports.SessionRef{ID: "ao-session-1"},
		Prompt:  "next task",
	}
	if args, ok, err := p.GetRestoreCommand(context.Background(), cfg); err != nil || ok || args != nil {
		t.Fatalf("restore without native ID = %v, %v, %v", args, ok, err)
	}
	cfg.Session.Metadata = map[string]string{ports.MetadataKeyAgentSessionID: " native-1 "}
	cfg.Config.Model = "model-2"
	args, ok, err := p.GetRestoreCommand(context.Background(), cfg)
	want := []string{"codebuddy", "--model", "model-2", "--resume", "native-1"}
	if err != nil || !ok || !reflect.DeepEqual(args, want) {
		t.Fatalf("restore = %v, %v, %v, want %v", args, ok, err, want)
	}
}

func TestLaunchAndRestoreTrustOnlyTheSelectedWorkspace(t *testing.T) {
	p := &Plugin{resolvedBinary: "codebuddy"}
	workspace := filepath.Join(t.TempDir(), `workspace with spaces and "quotes"`)
	launch, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	restore, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			WorkspacePath: workspace,
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "native-1"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore=%v, err=%v", ok, err)
	}
	for _, args := range [][]string{launch, restore} {
		if len(args) < 3 || args[1] != "--settings" {
			t.Fatalf("missing process-local trust overlay: %v", args)
		}
		var settings map[string]json.RawMessage
		if err := json.Unmarshal([]byte(args[2]), &settings); err != nil {
			t.Fatal(err)
		}
		var directories []string
		if err := json.Unmarshal(settings["trustedDirectories"], &directories); err != nil {
			t.Fatal(err)
		}
		if len(settings) != 1 || !reflect.DeepEqual(directories, []string{workspace}) {
			t.Fatalf("trust overlay=%s, want only selected workspace", args[2])
		}
	}
}

func TestHooksPreserveUserSettingsAndAreIdempotent(t *testing.T) {
	p := New()
	workspace := t.TempDir()
	settings := codeBuddySettingsPath(workspace)
	if err := os.MkdirAll(filepath.Dir(settings), 0o750); err != nil {
		t.Fatal(err)
	}
	seed := `{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}}`
	if err := os.WriteFile(settings, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Theme string `json:"theme"`
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Theme != "dark" || !strings.Contains(string(data), "echo mine") {
		t.Fatalf("user settings lost: %s", data)
	}
	for _, spec := range managedHooks {
		count := 0
		for _, group := range result.Hooks[spec.Event] {
			for _, hook := range group.Hooks {
				if hook.Command == spec.Command {
					count++
				}
			}
		}
		if count != 1 {
			t.Fatalf("%s count=%d, want one", spec.Command, count)
		}
	}
	if ok, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || !ok {
		t.Fatalf("installed=%v, err=%v", ok, err)
	}
	if err := p.UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), hookCommand) || !strings.Contains(string(data), "echo mine") || !strings.Contains(string(data), "dark") {
		t.Fatalf("uninstall changed user settings: %s", data)
	}
}

func TestActivityLifecycle(t *testing.T) {
	for _, tc := range []struct {
		event, payload string
		want           domain.ActivityState
		ok             bool
	}{
		{"session-start", `{"source":"resume"}`, "", false},
		{"user-prompt-submit", `{}`, domain.ActivityActive, true},
		{"pre-tool-use", `{}`, domain.ActivityActive, true},
		{"permission-request", `{}`, domain.ActivityBlocked, true},
		{"notification", `{"notification_type":"permission_prompt"}`, domain.ActivityBlocked, true},
		{"notification", `{"notification_type":"agent_needs_input"}`, domain.ActivityWaitingInput, true},
		{"notification", `{"notification_type":"idle_prompt"}`, domain.ActivityIdle, true},
		{"stop", `{}`, domain.ActivityIdle, true},
		{"stop-failure", `{}`, domain.ActivityIdle, true},
		{"session-end", `{"reason":"clear"}`, "", false},
		{"session-end", `{"reason":"other"}`, "", false},
		{"notification", `{`, "", false},
	} {
		t.Run(tc.event+tc.payload, func(t *testing.T) {
			got, ok := DeriveActivityState(tc.event, []byte(tc.payload))
			if got != tc.want || ok != tc.ok {
				t.Fatalf("activity=%q, %v, want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestAuthNeverClaimsEnvironmentCredentialsAreAuthorized(t *testing.T) {
	t.Setenv("CODEBUDDY_API_KEY", "not-a-valid-key")
	got, err := New().AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusUnknown {
		t.Fatalf("auth=%q, err=%v", got, err)
	}
}

func TestCancelledOperationsReturnContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().GetLaunchCommand(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("launch err=%v", err)
	}
	if _, _, err := New().GetRestoreCommand(ctx, ports.RestoreConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("restore err=%v", err)
	}
	if _, err := New().AuthStatus(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("auth err=%v", err)
	}
}
