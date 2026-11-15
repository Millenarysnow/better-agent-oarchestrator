package opencodev2acp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type handoffACPPlugin struct{}

func (handoffACPPlugin) ResolveBinary(context.Context) (string, error) { return os.Executable() }
func (handoffACPPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}

func TestTerminalHandoffLoadsAndRefreshesExactNativeHistory(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "history", true: "failed load"}[fail], func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			calls := filepath.Join(t.TempDir(), "calls.log")
			env := map[string]string{
				"AO_TEST_OPENCODE_V2_ACP_PROVIDER": "1", "AO_TEST_OPENCODE_VERSION": "2.3.0",
				"AO_TEST_OPENCODE_V2_LOAD": "1", "AO_TEST_OPENCODE_V2_ACP_CALLS": calls,
			}
			if fail {
				env["AO_TEST_OPENCODE_V2_LOAD_FAILURE"] = "1"
			}
			id, _, err := opencodev2.New().NativeConversationID(context.Background(), ports.SessionRef{
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ses_terminal"},
			}, domain.SessionModeTUI, "")
			if err != nil {
				t.Fatal(err)
			}
			conv, err := New(handoffACPPlugin{}, nil).Resume(context.Background(), ports.ChatResumeConfig{
				SessionID: "v2-handoff", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
				ProviderConversationID: id, Env: env,
			})
			if fail {
				if !errors.Is(err, ports.ErrChatHistoryLoadFailed) {
					t.Fatalf("failed native replay = %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conv.(ports.ChatProviderTerminator).Terminate() }()
				if conv.ProviderConversationID() != id {
					t.Fatalf("provider changed the captured terminal: %q", conv.ProviderConversationID())
				}
				history, err := conv.(ports.ChatHistoryReader).ReadHistory(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var text []string
				for _, event := range history {
					if event.Kind == ports.ChatEventUserMessageCompleted || event.Kind == ports.ChatEventMessageCompleted {
						text = append(text, event.Text)
					}
				}
				if !reflect.DeepEqual(text, []string{"Existing terminal question", "Existing terminal answer"}) {
					t.Fatalf("native replay = %q", text)
				}
				refreshed, err := conv.(ports.ChatHistoryRefresher).RefreshHistory(context.Background())
				if err != nil || len(history) != len(refreshed) {
					t.Fatalf("refresh changed native replay: %v", err)
				}
				for i, event := range history {
					if event.Kind != refreshed[i].Kind || event.ProviderEventID != refreshed[i].ProviderEventID || event.Text != refreshed[i].Text {
						t.Fatalf("refresh changed native event %d", i)
					}
				}
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "session/load:ses_terminal\n") || strings.Contains(string(data), "session/new:") || strings.Contains(string(data), "session/load:ao-harness:") {
				t.Fatalf("handoff did not load the original raw identity:\n%s", data)
			}
		})
	}
}
