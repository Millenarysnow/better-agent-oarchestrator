package codebuddy

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	settingsDirName  = ".codebuddy"
	settingsFileName = "settings.local.json"
	hookCommand      = "ao hooks codebuddy "
	hookTimeout      = 30
)

var managedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Command: hookCommand + "session-start"},
	{Event: "UserPromptSubmit", Command: hookCommand + "user-prompt-submit"},
	{Event: "PreToolUse", Command: hookCommand + "pre-tool-use"},
	{Event: "PostToolUse", Command: hookCommand + "post-tool-use"},
	{Event: "PermissionRequest", Command: hookCommand + "permission-request"},
	{Event: "Notification", Command: hookCommand + "notification"},
	{Event: "Stop", Command: hookCommand + "stop"},
	{Event: "StopFailure", Command: hookCommand + "stop-failure"},
}

var codeBuddyHooks = hooksjson.Manager{
	Label:         adapterID,
	CommandPrefix: hookCommand,
	Timeout:       hookTimeout,
	Path:          codeBuddySettingsPath,
	Managed:       managedHooks,
}

func codeBuddySettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, settingsDirName, settingsFileName)
}

// GetAgentHooks merges AO lifecycle callbacks into the ignored workspace-local
// settings file and preserves the user's project hooks and unrelated settings.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return codeBuddyHooks.Install(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes only AO-owned CodeBuddy callbacks.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return codeBuddyHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether this workspace contains AO-owned callbacks.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return codeBuddyHooks.AreInstalled(ctx, workspacePath)
}
