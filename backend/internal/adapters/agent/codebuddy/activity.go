package codebuddy

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps the CodeBuddy lifecycle events AO installs onto
// durable activity facts. SessionStart is metadata-only because it also fires
// on resume, compact, and clear.
func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit", "pre-tool-use", "post-tool-use":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityBlocked, true
	case "stop", "stop-failure":
		return domain.ActivityIdle, true
	case "notification":
		var p struct {
			NotificationType string `json:"notification_type"`
		}
		if json.Unmarshal(payload, &p) != nil {
			return "", false
		}
		switch p.NotificationType {
		case "idle_prompt", "agent_completed":
			return domain.ActivityIdle, true
		case "agent_needs_input":
			return domain.ActivityWaitingInput, true
		case "permission_prompt":
			return domain.ActivityBlocked, true
		}
	}
	return "", false
}
