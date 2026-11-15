package sessionmanager

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type openCodeTransitionAgent struct{ transitionAgent }

func (openCodeTransitionAgent) NativeConversationID(ctx context.Context, ref ports.SessionRef, mode domain.SessionMode, providerID string) (string, bool, error) {
	return opencodev2.New().NativeConversationID(ctx, ref, mode, providerID)
}

type openCodeSurfaceTransitionAgent struct{ openCodeTransitionAgent }

func (openCodeSurfaceTransitionAgent) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	return (transitionSurfaceAgent{}).InspectTerminalSurface(output)
}

func TestOpenCode2HandoffCleansTheThreadSelectedAfterDrain(t *testing.T) {
	m, st, rt, chat, _ := newTransitionManager(t, domain.SessionModeTUI)
	useFastInterfaceTransitionTimings(m)
	s := &openCodeTransitionStore{transitionStore: st}
	m.store = s
	m.agents = singleAgent{agent: openCodeSurfaceTransitionAgent{}}
	rec := st.sessions["session-1"]
	rec.Harness = domain.HarnessOpenCodeV2
	rec.Metadata.AgentSessionID = "ses_before"
	rec.Activity.LastActivityAt = time.Now().Add(-time.Minute)
	st.sessions[rec.ID] = rec
	rt.aliveByHandle = map[string]bool{"runtime-1": true}
	rt.outputForCall = func(_ int) string {
		current := st.sessions[rec.ID]
		current.Metadata.AgentSessionID = "ses_after"
		st.sessions[rec.ID] = current
		return idleTerminalOutput
	}
	tr, err := m.StartInterfaceTransition(context.Background(), rec.ID, domain.SessionModeChat,
		domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	if settled := awaitTransition(t, st, tr.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("handoff = %+v", settled)
	}
	if s.deletedNativeID != "ses_after" || chat.start.ProviderConversationID != "ao-harness:opencode-v2:ses_after" {
		t.Fatalf("cleaned %q after importing %q", s.deletedNativeID, chat.start.ProviderConversationID)
	}
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
