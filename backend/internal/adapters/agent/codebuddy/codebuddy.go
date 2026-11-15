// Package codebuddy implements the CodeBuddy Code terminal-agent adapter.
package codebuddy

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "codebuddy"

// Plugin adapts the user's CodeBuddy Code CLI to AO's terminal-agent contract.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New constructs a CodeBuddy Code adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)

// Manifest describes the CodeBuddy Code CLI adapter.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "CodeBuddy Code",
		Description: "Run CodeBuddy Code worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec exposes CodeBuddy's documented model override.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `codebuddy --model`.")
}

// GetLaunchCommand starts an interactive CodeBuddy session. AO sends the task
// after the terminal is ready so leading dashes in task text cannot be parsed
// as CLI flags. Standing instructions are delivered by the SessionStart hook.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	binary, err := p.binary(ctx)
	if err != nil {
		return nil, err
	}
	return launchArgs(binary, cfg.Config, cfg.Permissions, cfg.WorkspacePath)
}

func launchArgs(binary string, config ports.AgentConfig, permission ports.PermissionMode, workspace string) ([]string, error) {
	args, err := commandPrefix(binary)
	if err != nil {
		return nil, err
	}
	// AO owns and deliberately selected this workspace. Trust only that exact
	// directory through a process-local settings overlay, never the user profile
	// or trustAll. Otherwise the initial task could answer the trust dialog.
	if workspace != "" {
		settings, err := json.Marshal(struct {
			TrustedDirectories []string `json:"trustedDirectories"`
		}{TrustedDirectories: []string{workspace}})
		if err != nil {
			return nil, err
		}
		args = append(args, "--settings", string(settings))
	}
	appendPermissionFlag(&args, permission)
	agentbase.AppendModelFlag(&args, config, "--model")
	return args, nil
}

func appendPermissionFlag(args *[]string, permission ports.PermissionMode) {
	mode := ""
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeDefault:
		return
	case ports.PermissionModeAcceptEdits:
		mode = "acceptEdits"
	case ports.PermissionModeAuto:
		mode = "auto"
	case ports.PermissionModeBypassPermissions:
		mode = "bypassPermissions"
	}
	if mode != "" {
		*args = append(*args, "--permission-mode", mode)
	}
}

// GetPromptDeliveryStrategy tells AO to deliver the initial task after startup.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

// PromptReadinessHints waits briefly for the CLI's interactive surface before
// AO sends the task; timeout still falls back to delivery if the banner changes.
func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: 750 * time.Millisecond,
		Patterns:     []string{"CodeBuddy"},
		PollInterval: 200 * time.Millisecond,
		Timeout:      8 * time.Second,
		Lines:        80,
	}, nil
}

// GetRestoreCommand resumes only the CodeBuddy conversation AO captured from
// its SessionStart hook. No AO identifier is passed as a provider session ID.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	nativeID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if nativeID == "" {
		return nil, false, nil
	}
	binary, err := p.binary(ctx)
	if err != nil {
		return nil, false, err
	}
	args, err := launchArgs(binary, cfg.Config, cfg.Permissions, cfg.Session.WorkspacePath)
	if err != nil {
		return nil, false, err
	}
	return append(args, "--resume", nativeID), true, nil
}

// SessionInfo returns standard metadata captured by AO's hooks.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// ResolveBinary finds the user's CodeBuddy Code executable.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) { return p.binary(ctx) }

// InvalidateBinaryResolution forgets the cached executable path.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

// AuthStatus does not claim that configured credentials are valid. The
// official CLI has no documented, reliable non-interactive local auth probe.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return ports.AgentAuthStatusUnknown, nil
}

func (p *Plugin) binary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, codeBuddyBinarySpec)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

var codeBuddyBinarySpec = binaryutil.BinarySpec{
	Label:         "codebuddy",
	Names:         []string{"codebuddy"},
	WinNames:      []string{"codebuddy.cmd", "codebuddy.exe", "codebuddy"},
	UnixPaths:     []string{"/usr/local/bin/codebuddy", "/opt/homebrew/bin/codebuddy"},
	UnixHomePaths: [][]string{{".local", "bin", "codebuddy"}},
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "codebuddy.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "codebuddy.exe"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"codebuddy", "bin", "codebuddy.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "codebuddy.exe"}},
	},
}
