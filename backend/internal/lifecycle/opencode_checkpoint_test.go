package lifecycle

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestOpenCode2ObservationalStopPreservesCheckpoint(t *testing.T) {
	for _, debt := range []bool{false, true} {
		store := newFakeStore()
		store.sessions["qa-1"] = domain.SessionRecord{
			ID: "qa-1", Harness: domain.HarnessOpenCodeV2, Mode: domain.SessionModeTUI,
			Metadata: domain.SessionMetadata{
				RuntimeLaunchID: "launch", AgentSessionID: "ses_1", AgentSessionIDLaunchID: "launch",
				ConversationCheckpointUnsettled: debt,
			},
		}
		if err := New(store, nil).ApplyActivitySignal(context.Background(), "qa-1", ports.ActivitySignal{
			Event: "stop", LaunchID: "launch", AgentSessionID: "ses_1", Valid: true, State: domain.ActivityIdle,
		}); err != nil {
			t.Fatal(err)
		}
		if got := store.sessions["qa-1"].Metadata.ConversationCheckpointUnsettled; got != debt {
			t.Fatalf("observational Stop changed retained debt from %v to %v", debt, got)
		}
	}
}

func TestOpenCode2RawHookDoesNotDischargeTaggedConversationDebt(t *testing.T) {
	store := newFakeStore()
	store.sessions["qa-1"] = domain.SessionRecord{
		ID: "qa-1", Harness: domain.HarnessOpenCodeV2, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{
			RuntimeLaunchID: "launch", AgentSessionID: "ao-harness:opencode-v2:ses_1", AgentSessionIDLaunchID: "launch",
			ConversationCheckpointUnsettled: true, LatestUserPrompt: "must retain this input",
		},
	}
	if err := New(store, nil).ApplyActivitySignal(context.Background(), "qa-1", ports.ActivitySignal{
		Event: "session-start", LaunchID: "launch", AgentSessionID: "ses_1", Valid: true, State: domain.ActivityIdle,
	}); err != nil {
		t.Fatal(err)
	}
	got := store.sessions["qa-1"].Metadata
	if got.AgentSessionID != "ses_1" || !got.ConversationCheckpointUnsettled || got.LatestUserPrompt != "must retain this input" {
		t.Fatalf("equivalent hook identity erased checkpoint: %+v", got)
	}
}
