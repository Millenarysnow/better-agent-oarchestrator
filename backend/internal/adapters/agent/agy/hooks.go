package agy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

const (
	agyHooksDirName      = ".agents"
	agyHooksFileName     = "hooks.json"
	agyManagedHookName   = "agent-orchestrator"
	agyHookCommandPrefix = "ao hooks agy "
	agyHookTimeout       = 30
	agySessionAgentName  = "ao-session"
	agySessionAgentFile  = "ao-session.md"
	agySessionAgentMark  = "<!-- managed by agent-orchestrator: agy session agent -->"
	agySkillMarkerFile   = ".using-ao.ao-managed"
	agySkillMarker       = "agent-orchestrator: managed agy using-ao skill"
)

type agyHookEntry struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type agyMatcherGroup struct {
	Matcher *string        `json:"matcher,omitempty"`
	Hooks   []agyHookEntry `json:"hooks"`
}

type agyNamedHook struct {
	PreInvocation []agyHookEntry    `json:"PreInvocation"`
	PostToolUse   []agyMatcherGroup `json:"PostToolUse"`
	Stop          []agyHookEntry    `json:"Stop"`
}

// GetAgentHooks installs AO's activity hook, the shared using-ao skill, and a
// primary AGY custom agent carrying this session's system prompt. Other hooks
// and files in the workspace remain untouched.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("agy.GetAgentHooks: WorkspacePath is required")
	}

	hooksPath := agyHooksPath(cfg.WorkspacePath)
	topLevel, err := readAgyHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("agy.GetAgentHooks: %w", err)
	}
	managedJSON, err := json.Marshal(managedAgyHook())
	if err != nil {
		return fmt.Errorf("agy.GetAgentHooks: encode managed hook: %w", err)
	}
	if raw, ok := topLevel[agyManagedHookName]; ok {
		var existing agyNamedHook
		if err := json.Unmarshal(raw, &existing); err != nil || !reflect.DeepEqual(existing, managedAgyHook()) {
			return fmt.Errorf("agy.GetAgentHooks: refusing to overwrite non-Orchestrator hook named %q", agyManagedHookName)
		}
	}
	if domain.NormalizeSessionMode(cfg.SessionMode) == domain.SessionModeChat {
		// ACP already delivers the standing instructions through its preamble.
		// A TUI->Chat handoff reuses this workspace, so release the TUI profile.
		if err := errors.Join(removeAgySessionAgent(cfg.WorkspacePath), uninstallUsingAOSkill(cfg.WorkspacePath)); err != nil {
			return fmt.Errorf("agy.GetAgentHooks: remove terminal instructions: %w", err)
		}
		topLevel[agyManagedHookName] = managedJSON
		if err := hookutil.EnsureWorkspaceGitignore(agyAgentsDir(cfg.WorkspacePath), agyHooksFileName); err != nil {
			return fmt.Errorf("agy.GetAgentHooks: gitignore: %w", err)
		}
		return writeAgyHooks(hooksPath, topLevel)
	}
	if err := checkAgySessionAgentWritable(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("agy.GetAgentHooks: %w", err)
	}
	if err := checkAgySkillWritable(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("agy.GetAgentHooks: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(agyAgentsDir(cfg.WorkspacePath),
		agyHooksFileName,
		filepath.ToSlash(filepath.Join("agents", agySessionAgentFile)),
		filepath.ToSlash(filepath.Join("skills", skillassets.SkillName)),
		filepath.ToSlash(filepath.Join("skills", agySkillMarkerFile)),
	); err != nil {
		return fmt.Errorf("agy.GetAgentHooks: gitignore: %w", err)
	}
	if err := installUsingAOSkill(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("agy.GetAgentHooks: install using-ao skill: %w", err)
	}
	if err := installAgySessionAgent(cfg); err != nil {
		cleanupErr := errors.Join(removeAgySessionAgent(cfg.WorkspacePath), uninstallUsingAOSkill(cfg.WorkspacePath))
		return fmt.Errorf("agy.GetAgentHooks: install session agent: %w", errors.Join(err, cleanupErr))
	}
	topLevel[agyManagedHookName] = managedJSON
	if err := writeAgyHooks(hooksPath, topLevel); err != nil {
		cleanupErr := errors.Join(removeAgySessionAgent(cfg.WorkspacePath), uninstallUsingAOSkill(cfg.WorkspacePath))
		return fmt.Errorf("agy.GetAgentHooks: write hooks: %w", errors.Join(err, cleanupErr))
	}
	return nil
}

// UninstallHooks removes only AO-owned AGY configuration from the workspace.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("agy.UninstallHooks: workspacePath is required")
	}

	hooksPath := agyHooksPath(workspacePath)
	topLevel, err := readAgyHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("agy.UninstallHooks: read %s: %w", hooksPath, err)
	}
	if raw, ok := topLevel[agyManagedHookName]; ok {
		var installed agyNamedHook
		if err := json.Unmarshal(raw, &installed); err == nil && reflect.DeepEqual(installed, managedAgyHook()) {
			delete(topLevel, agyManagedHookName)
			if err := writeAgyHooks(hooksPath, topLevel); err != nil {
				return fmt.Errorf("agy.UninstallHooks: remove managed hook: %w", err)
			}
		}
	}
	if err := removeAgySessionAgent(workspacePath); err != nil {
		return fmt.Errorf("agy.UninstallHooks: remove session agent: %w", err)
	}
	if err := uninstallUsingAOSkill(workspacePath); err != nil {
		return fmt.Errorf("agy.UninstallHooks: remove using-ao skill: %w", err)
	}
	return nil
}

// CleanupWorkspace removes AO-owned files when preparation rolls back, the
// workspace is torn down, or a TUI provider switch leaves AGY.
func (p *Plugin) CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return p.UninstallHooks(ctx, cfg.WorkspacePath)
}

// AreHooksInstalled reports whether AO's exact managed AGY hook is installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("agy.AreHooksInstalled: workspacePath is required")
	}

	hooksPath := agyHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("agy.AreHooksInstalled: stat %s: %w", hooksPath, err)
	}
	topLevel, err := readAgyHooks(hooksPath)
	if err != nil {
		return false, fmt.Errorf("agy.AreHooksInstalled: %w", err)
	}
	raw, ok := topLevel[agyManagedHookName]
	if !ok {
		return false, nil
	}
	var installed agyNamedHook
	if err := json.Unmarshal(raw, &installed); err != nil {
		return false, fmt.Errorf("agy.AreHooksInstalled: unmarshal hook: %w", err)
	}
	return reflect.DeepEqual(installed, managedAgyHook()), nil
}

func agyHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, agyHooksDirName, agyHooksFileName)
}

func agyAgentsDir(workspacePath string) string {
	return filepath.Join(workspacePath, agyHooksDirName)
}

func agySessionAgentPath(workspacePath string) string {
	return filepath.Join(agyAgentsDir(workspacePath), "agents", agySessionAgentFile)
}

func agySkillsDir(workspacePath string) string {
	return filepath.Join(agyAgentsDir(workspacePath), "skills")
}

func agyUsingAOSkillDir(workspacePath string) string {
	return filepath.Join(agySkillsDir(workspacePath), skillassets.SkillName)
}

func agyUsingAOSkillMarkerPath(workspacePath string) string {
	return filepath.Join(agySkillsDir(workspacePath), agySkillMarkerFile)
}

func installAgySessionAgent(cfg ports.WorkspaceHookConfig) error {
	path := agySessionAgentPath(cfg.WorkspacePath)

	prompt := cfg.SystemPrompt
	if strings.TrimSpace(prompt) == "" && strings.TrimSpace(cfg.SystemPromptFile) != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile) //nolint:gosec // AO-owned system prompt path
		if err != nil {
			return fmt.Errorf("read AO system prompt: %w", err)
		}
		prompt = string(data)
	}
	if strings.TrimSpace(cfg.DataDir) != "" {
		skillSource := filepath.ToSlash(skillassets.Dir(cfg.DataDir))
		skillTarget := filepath.ToSlash(agyUsingAOSkillDir(cfg.WorkspacePath))
		prompt = strings.ReplaceAll(prompt, skillSource, skillTarget)
	}

	content := "---\n" +
		"name: " + agySessionAgentName + "\n" +
		"description: AO-managed primary agent for this Orchestrator session.\n" +
		"mainAgent: true\n" +
		"subagent: false\n" +
		"inheritCustomizations: true\n" +
		"---\n" + agySessionAgentMark + "\n\n# AO Session Instructions\n\n" + prompt
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create custom agent directory: %w", err)
	}
	if err := hookutil.AtomicWriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write custom agent: %w", err)
	}
	return hookutil.EnsureWorkspaceGitignore(filepath.Dir(path), agySessionAgentFile)
}

func checkAgySessionAgentWritable(workspacePath string) error {
	path := agySessionAgentPath(workspacePath)
	data, err := os.ReadFile(path) //nolint:gosec // path is rooted in the AO workspace
	if errors.Is(err, os.ErrNotExist) || (err == nil && strings.Contains(string(data), agySessionAgentMark)) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read existing custom agent: %w", err)
	}
	return fmt.Errorf("refusing to overwrite non-Orchestrator custom agent at %s", path)
}

func removeAgySessionAgent(workspacePath string) error {
	path := agySessionAgentPath(workspacePath)
	data, err := os.ReadFile(path) //nolint:gosec // path is rooted in the AO workspace
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !strings.Contains(string(data), agySessionAgentMark) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func installUsingAOSkill(workspacePath string) error {
	skillDir := agyUsingAOSkillDir(workspacePath)
	markerPath := agyUsingAOSkillMarkerPath(workspacePath)
	if err := os.MkdirAll(agySkillsDir(workspacePath), 0o750); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}
	if err := hookutil.AtomicWriteFile(markerPath, []byte(agySkillMarker+"\n"), 0o600); err != nil {
		return fmt.Errorf("write skill marker: %w", err)
	}
	if err := skillassets.Materialize(skillDir); err != nil {
		return err
	}
	byDir := map[string][]string{}
	if err := filepath.WalkDir(skillDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], entry.Name())
		return nil
	}); err != nil {
		return err
	}
	for dir, names := range byDir {
		if err := hookutil.EnsureWorkspaceGitignore(dir, names...); err != nil {
			return err
		}
	}
	return hookutil.EnsureWorkspaceGitignore(agySkillsDir(workspacePath), agySkillMarkerFile)
}

func checkAgySkillWritable(workspacePath string) error {
	skillDir := agyUsingAOSkillDir(workspacePath)
	info, err := os.Stat(skillDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat skill directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to overwrite non-directory at %s", skillDir)
	}
	managed, err := isManagedAgySkill(workspacePath)
	if err != nil {
		return err
	}
	if !managed {
		return fmt.Errorf("refusing to overwrite non-Orchestrator skill at %s", skillDir)
	}
	return nil
}

func uninstallUsingAOSkill(workspacePath string) error {
	managed, err := isManagedAgySkill(workspacePath)
	if err != nil || !managed {
		return err
	}
	if err := os.RemoveAll(agyUsingAOSkillDir(workspacePath)); err != nil {
		return err
	}
	if err := os.Remove(agyUsingAOSkillMarkerPath(workspacePath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func isManagedAgySkill(workspacePath string) (bool, error) {
	data, err := os.ReadFile(agyUsingAOSkillMarkerPath(workspacePath)) //nolint:gosec // path is rooted in the AO workspace
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), agySkillMarker), nil
}

func managedAgyHook() agyNamedHook {
	matcher := "*"
	entry := func(event string) agyHookEntry {
		return agyHookEntry{Type: "command", Command: agyHookCommandPrefix + event, Timeout: agyHookTimeout}
	}
	return agyNamedHook{
		PreInvocation: []agyHookEntry{entry("pre-invocation")},
		PostToolUse: []agyMatcherGroup{{
			Matcher: &matcher,
			Hooks:   []agyHookEntry{entry("post-tool-use")},
		}},
		Stop: []agyHookEntry{entry("stop")},
	}
}

// readAgyHooks preserves unowned top-level entries as raw JSON values. Missing
// and blank files are treated as empty configuration.
func readAgyHooks(hooksPath string) (map[string]json.RawMessage, error) {
	topLevel := map[string]json.RawMessage{}
	data, err := os.ReadFile(hooksPath) //nolint:gosec // caller-owned workspace path
	if errors.Is(err, os.ErrNotExist) {
		return topLevel, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", hooksPath, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return topLevel, nil
	}
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return nil, fmt.Errorf("parse %s: %w", hooksPath, err)
	}
	if topLevel == nil {
		return nil, fmt.Errorf("parse %s: top-level value must be an object", hooksPath)
	}
	return topLevel, nil
}

func writeAgyHooks(hooksPath string, topLevel map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return fmt.Errorf("create hook dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", hooksPath, err)
	}
	data = append(data, '\n')
	if err := hookutil.AtomicWriteFile(hooksPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", hooksPath, err)
	}
	return nil
}
