package opencodev2

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeConversationHandoffIdentity(t *testing.T) {
	const tagged = "ao-harness:opencode-v2:ses_original"
	for _, tc := range []struct {
		name, terminal, provider string
		mode                     domain.SessionMode
		want                     string
		fail                     bool
	}{
		{name: "existing terminal", terminal: "ses_original", mode: domain.SessionModeTUI, want: tagged},
		{name: "returned terminal", terminal: tagged, mode: domain.SessionModeTUI, want: tagged},
		{name: "chat uses provider", terminal: "ses_wrong", provider: tagged, mode: domain.SessionModeChat, want: tagged},
		{name: "untouched terminal", mode: domain.SessionModeTUI},
		{name: "untouched chat", mode: domain.SessionModeChat},
		{name: "raw chat refused", provider: "ses_original", mode: domain.SessionModeChat, fail: true},
		{name: "v1 chat refused", provider: "ao-harness:opencode:ses_original", mode: domain.SessionModeChat, fail: true},
		{name: "v1 terminal refused", terminal: "ao-harness:opencode:ses_original", mode: domain.SessionModeTUI, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok, err := New().NativeConversationID(context.Background(), ports.SessionRef{
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: tc.terminal},
			}, tc.mode, tc.provider)
			if (err != nil) != tc.fail || id != tc.want || ok != (tc.want != "") {
				t.Fatalf("handoff = %q, %v, %v", id, ok, err)
			}
			if ok {
				raw, err := terminalNativeID(id)
				if err != nil || raw != "ses_original" {
					t.Fatalf("terminal round trip = %q, %v", raw, err)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := New().NativeConversationID(ctx, ports.SessionRef{}, domain.SessionModeChat, tagged); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestNativeConversationHandoffRestore(t *testing.T) {
	fakeBinary(t, "2.3.0")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cmd, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ao-harness:opencode-v2:ses_original"}},
	})
	if err != nil || !ok || !strings.Contains(strings.Join(cmd, " "), "--session ses_original") || strings.Contains(strings.Join(cmd, " "), "ao-harness:") {
		t.Fatalf("restore = %q, %v, %v", cmd, ok, err)
	}
}
