package opencodev2

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/opencodeidentity"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentInterfaceHandoff = (*Plugin)(nil)

// NativeConversationID returns the same harness-scoped handle from either
// controller. ACP validates that handle; terminal commands unwrap it locally.
func (*Plugin) NativeConversationID(ctx context.Context, session ports.SessionRef, mode domain.SessionMode, providerID string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	id := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID])
	if mode == domain.SessionModeChat {
		id = strings.TrimSpace(providerID)
		if id == "" {
			return "", false, nil
		}
		// Unlike legacy TUI rows, a persisted v2 Chat must carry its major tag.
		raw, err := opencodeidentity.Decode(domain.HarnessOpenCodeV2, id)
		if err != nil {
			return "", false, err
		}
		return opencodeidentity.Encode(domain.HarnessOpenCodeV2, raw), true, nil
	}
	raw, err := terminalNativeID(id)
	if err != nil || raw == "" {
		return "", false, err
	}
	return opencodeidentity.Encode(domain.HarnessOpenCodeV2, raw), true, nil
}

// Raw terminal identities predate Chat handoff. Only this adapter boundary
// admits them; opencodeidentity.Decode remains strict for persisted v2 Chat.
func terminalNativeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, domain.HarnessConversationIDPrefix) {
		return opencodeidentity.Decode(domain.HarnessOpenCodeV2, id)
	}
	return id, nil
}
