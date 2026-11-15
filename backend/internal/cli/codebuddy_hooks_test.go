package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeBuddySessionStartDeliversStandingInstructionsAndNativeIdentity(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	promptDir := filepath.Join(cfg.dataDir, "prompts", "ao-7")
	if err := os.MkdirAll(promptDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "system.md"), []byte("Follow the AO worker role.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"codebuddy-native-1","source":"resume"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codebuddy", "session-start")
	if err != nil {
		t.Fatal(err)
	}
	var contextOutput sessionStartHookOutput
	if err := json.Unmarshal([]byte(out), &contextOutput); err != nil {
		t.Fatalf("decode context: %v (%s)", err, out)
	}
	if contextOutput.HookSpecificOutput.HookEventName != "SessionStart" || contextOutput.HookSpecificOutput.AdditionalContext != "Follow the AO worker role." {
		t.Fatalf("standing instructions = %+v", contextOutput)
	}
	if capture.hits != 1 {
		t.Fatalf("daemon calls = %d, want one", capture.hits)
	}
	var request setActivityAPIRequest
	if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
		t.Fatal(err)
	}
	assertActivityRequest(t, request, setActivityAPIRequest{Event: "session-start", AgentSessionID: "codebuddy-native-1"})
}

func TestCodeBuddyPermissionHookReportsBlockedWithoutApprovingTheTool(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-1","tool_name":"Bash","tool_use_id":"tool-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codebuddy", "permission-request")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("observational permission hook returned a tool decision: %s", out)
	}
	var request setActivityAPIRequest
	if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
		t.Fatal(err)
	}
	assertActivityRequest(t, request, setActivityAPIRequest{
		State: "blocked", Event: "permission-request", AgentSessionID: "native-1", ToolName: "Bash", ToolUseID: "tool-1",
	})
}
