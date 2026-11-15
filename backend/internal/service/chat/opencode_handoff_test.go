package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestOpenCode2HandoffCheckpointAdmission(t *testing.T) {
	const handle = "ao-harness:opencode-v2:ses_terminal"
	for _, scenario := range []string{
		"legacy activity", "trusted raw identity", "trusted wrapped identity", "wrong identity",
		"retained prompt", "retained turn", "retained submission time", "other harness",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			rec, _, err := st.GetSession(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			rec.Harness = domain.HarnessOpenCodeV2
			rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = handle, handle
			rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointEmpty
			rec.Metadata.ConversationCheckpointUnsettled = true
			wantPass := false
			switch scenario {
			case "legacy activity":
				wantPass = true
			case "trusted raw identity", "trusted wrapped identity", "wrong identity":
				rec.Metadata.ConversationCheckpointUnsettled = false
				rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
				rec.Metadata.ConversationCheckpointGeneration = "source-tui-launch"
				rec.Metadata.ConversationCheckpointNativeID = "ses_terminal"
				if scenario == "trusted wrapped identity" {
					rec.Metadata.ConversationCheckpointNativeID = handle
				}
				if scenario == "wrong identity" {
					rec.Metadata.ConversationCheckpointNativeID = "ao-harness:opencode:ses_terminal"
				}
				rec.Metadata.LatestUserPrompt = "Existing question"
				rec.Metadata.LatestAssistantUpdate = "Existing answer"
				wantPass = scenario != "wrong identity"
			case "retained prompt":
				rec.Metadata.LatestUserPrompt = "Missing newest user input"
			case "retained turn":
				rec.Metadata.ConversationCheckpointTurnID = "unresolved-turn"
			case "retained submission time":
				rec.Metadata.LatestUserPromptAt = time.Now()
			case "other harness":
				rec.Harness = domain.HarnessOpenCode
			}
			if err := st.UpdateSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
			base := newFakeConversation()
			base.providerConversationID = handle
			conv := &nativeHistoryConversation{fakeConversation: base, events: []ports.ChatEvent{
				{Kind: ports.ChatEventTurnStarted, ProviderEventID: "start", ProviderTurnID: "turn-1"},
				{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "user", ProviderTurnID: "turn-1", ProviderItemID: "msg_user", Text: "Existing question"},
				{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "answer", ProviderTurnID: "turn-1", ProviderItemID: "msg_answer", Text: "Existing answer"},
				{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "complete", ProviderTurnID: "turn-1", TurnState: domain.TurnStateCompleted},
			}}
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st, Reader: fullSnapshotReader(st),
				Drivers: fakeRegistry{driver: fakeDriver{conv: conv}}, Log: slog.New(slog.DiscardHandler), NewID: uuid.NewString,
			})
			t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
			ctrl, err := svc.Start(ctx, chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: rec.Harness, WorkspacePath: t.TempDir(),
				ProviderConversationID: handle, HistoryMode: ports.ChatHistoryRequired,
			})
			if !wantPass {
				if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
					t.Fatalf("retained checkpoint was not enforced: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, err := st.LoadConversationSnapshot(ctx, ctrl.ConversationID())
			if err != nil || len(rows.Messages) != 2 || rows.Messages[0].Text != "Existing question" || rows.Messages[1].Text != "Existing answer" {
				t.Fatalf("native history was not imported: %+v, %v", rows.Messages, err)
			}
		})
	}
}
