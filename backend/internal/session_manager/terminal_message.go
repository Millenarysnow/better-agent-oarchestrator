package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sendNativeTerminalMessage holds the same lifecycle admission as terminal pane
// writes, but never writes keystrokes or promotes a provider admission to a human
// prompt checkpoint. The adapter's nil error is the durable acceptance boundary.
func (m *Manager) sendNativeTerminalMessage(ctx context.Context, before domain.SessionRecord, sender ports.AgentTerminalMessageSender, message, deliveryID string) error {
	release, admitted := m.AcquireSessionInput(before.ID)
	if !admitted {
		return ErrOperationInProgress
	}
	defer release()
	rec, ok, err := m.store.GetSession(ctx, before.ID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if rec.IsTerminated {
		return ErrTerminated
	}
	if rec.Activity.State == domain.ActivityExited {
		return ErrAgentExited
	}
	if rec.Activity.State == domain.ActivityBlocked {
		return ErrAwaitingDecision
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || rec.Harness != before.Harness ||
		rec.Metadata.RuntimeLaunchID != before.Metadata.RuntimeLaunchID {
		return ErrOperationInProgress
	}
	if rec.Metadata.RuntimeHandleID == "" || rec.Metadata.RuntimeLaunchID == "" || rec.Metadata.AgentSessionID == "" ||
		rec.Metadata.AgentSessionIDLaunchID != rec.Metadata.RuntimeLaunchID {
		return fmt.Errorf("%w: native terminal identity is not confirmed for this launch", ErrStartupPending)
	}
	return sender.SendTerminalMessage(ctx, ports.TerminalMessageConfig{
		DataDir: m.dataDir, SessionID: rec.ID, LaunchID: rec.Metadata.RuntimeLaunchID,
		NativeSessionID: rec.Metadata.AgentSessionID, DeliveryID: deliveryID,
		Message: domain.WrapReportDelivery(deliveryID, message),
	})
}
