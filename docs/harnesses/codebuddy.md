# CodeBuddy Code CLI

AO supports the user's locally installed Tencent CodeBuddy Code CLI as a TUI
worker or orchestrator harness named `codebuddy`. It is distinct from the
WorkBuddy desktop app and WorkBuddy Managed Agents cloud service.

## Installation and login

Install the official CodeBuddy Code CLI, then complete its login flow outside AO:

```sh
npm install -g @tencent-ai/codebuddy-code
codebuddy
```

AO resolves `codebuddy` from PATH, common package-manager locations, the native
installer's Windows directory, and `~/.local/bin`. It never downloads a second
CLI or reads WorkBuddy's private credentials. The CodeBuddy login, model service
configuration, and account remain owned by CodeBuddy.

On Windows, AO launches the official npm package's Node entry directly so
settings and paths retain their original argument boundaries. Native `.exe`
installs are launched directly. Unrecognized custom batch wrappers require
using the official npm layout or a native executable.

## Session behavior

Select CodeBuddy Code as the project's worker/orchestrator, or spawn a TUI
session using `ao spawn --harness codebuddy --mode tui`.

AO starts the interactive `codebuddy` process in the session workspace. The
initial task is delivered after startup rather than passed as a positional
argument, so task text beginning with a dash is not interpreted as a CLI flag.
An optional model override is forwarded through `--model`; leaving it empty
uses the model configured in CodeBuddy.

AO trusts only the selected workspace through a process-local `--settings`
overlay. It does not enable `trustAll` or change the user's global trust settings.

| AO permission mode | CodeBuddy CLI option |
| --- | --- |
| default | no override; use CodeBuddy's configured default |
| accept-edits | `--permission-mode acceptEdits` |
| auto | `--permission-mode auto` |
| bypass-permissions | `--permission-mode bypassPermissions` |

CodeBuddy still enforces its own safety rules, including any high-risk
confirmation it retains in bypass mode. AO's hooks report activity and never
return permission approval decisions.

AO merges lifecycle hooks into the workspace's ignored
`.codebuddy/settings.local.json`, preserving unrelated settings and user hooks.
The `SessionStart` hook supplies AO standing instructions and captures the
native CodeBuddy session ID. Prompt, tool, permission, and completion
events feed AO's activity reporting. Process exit is observed by AO's supervisor:
CodeBuddy's `SessionEnd` can also mean a native session switch or clear and is
not proof that its terminal process exited. A captured session is restored with
`codebuddy --resume <native-session-id>`; an AO session ID is never used in its
place.

AO cannot verify CodeBuddy authentication with a documented local CLI probe,
so its readiness stays unknown until the provider itself runs. An unknown
probe does not mean credentials are missing or prevent a TUI launch.

## Current limits

Chat, interactive reviewer mode, automatic install/update, model catalog
discovery, and TUI/Chat history handoff are not registered. Although CodeBuddy
documents `--acp`, the installed version's approvals, cancellation, and native
resume behavior must be verified before it can meet AO's Chat admission floor.
See [the design](../技术方案/codebuddy-cli-harness.md) for that boundary.

The upstream hook implementation is Beta and can vary by CLI version. On
Windows it requires Git Bash (or the documented CodeBuddy shell override), not
PowerShell/cmd; see the [official hook reference](https://cloud.tencent.com/document/product/1831/137030).
