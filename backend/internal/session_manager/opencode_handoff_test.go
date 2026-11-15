package sessionmanager

import (
	"context"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type openCodeTransitionAgent struct{ transitionAgent }

func (openCodeTransitionAgent) NativeConversationID(ctx context.Context, ref ports.SessionRef, mode domain.SessionMode, providerID string) (string, bool, error) {
	return opencodev2.New().NativeConversationID(ctx, ref, mode, providerID)
}

func (openCodeTransitionAgent) NativeConversationExists(context.Context, ports.SessionRef, string, map[string]string) (bool, error) {
	return true, nil
}

type openCodeTransitionStore struct {
	*transitionStore
	deletedNativeID string
}

func (s *openCodeTransitionStore) DeleteTerminalTurnsForThread(_ context.Context, _ domain.SessionID, _ domain.AgentHarness, nativeID string) error {
	s.deletedNativeID = nativeID
	return nil
}

func TestOpenCode2InterfaceRoundTripPreservesIdentity(t *testing.T) {
	m, st, rt, chat, log := newTransitionManager(t, domain.SessionModeTUI)
	useFastInterfaceTransitionTimings(m)
	s := &openCodeTransitionStore{transitionStore: st}
	m.store = s
	m.agents = singleAgent{agent: openCodeTransitionAgent{}}
	rec := st.sessions["session-1"]
	rec.Harness = domain.HarnessOpenCodeV2
	rec.Metadata.AgentSessionID = "ses_terminal"
	st.sessions[rec.ID] = rec
	status, err := m.InterfaceTransitionStatus(context.Background(), rec.ID)
	if err != nil || !status.Supported {
		t.Fatalf("OpenCode 2 handoff remains disabled: %+v, %v", status, err)
	}
	tr, err := m.StartInterfaceTransition(context.Background(), rec.ID, domain.SessionModeChat,
		domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	if settled := awaitTransition(t, st, tr.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("Chat handoff = %+v", settled)
	}
	const handle = "ao-harness:opencode-v2:ses_terminal"
	if chat.start.ProviderConversationID != handle || chat.start.HistoryMode != ports.ChatHistoryRequired {
		t.Fatalf("target did not require original native history: %+v", chat.start)
	}
	if s.deletedNativeID != "ses_terminal" {
		t.Fatalf("cleanup used %q instead of the source terminal identity", s.deletedNativeID)
	}
	tr, err = m.StartInterfaceTransition(context.Background(), rec.ID, domain.SessionModeTUI,
		domain.SessionInterfaceTransitionInterrupt, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	if settled := awaitTransition(t, st, tr.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("Terminal handoff = %+v", settled)
	}
	got := st.sessions[rec.ID]
	if got.Mode != domain.SessionModeTUI || !domain.SameNativeConversationID(got.Harness, got.Metadata.AgentSessionID, "ses_terminal") {
		t.Fatalf("round trip selected another conversation: %+v", got)
	}
	if rt.created != 1 || fmt.Sprint(*log) != "[stop:tui:runtime-1 start:chat prepare:chat:interrupt stop:chat start:tui]" {
		t.Fatalf("controller overlap/order: %v, terminal starts=%d", *log, rt.created)
	}
}
