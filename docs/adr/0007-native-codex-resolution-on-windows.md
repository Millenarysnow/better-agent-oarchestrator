# ADR 0007: Native Codex Resolution on Windows

Status: accepted

## Context

The Codex TUI launch adds structured `-c hooks.*` overrides in
`backend/internal/adapters/agent/codex`. On Windows, passing those arguments
through a `codex.cmd` shim lets `cmd.exe` reinterpret embedded quotes and `&`
before the Codex CLI parses TOML. The resulting value is a string instead of
the expected hook sequence, and the detached hook command can be treated as a
separate shell command. PowerShell's call operator in the hook value only makes
that value executable after it reaches PowerShell intact; it cannot protect it
from an earlier `cmd.exe` parse.

The Codex resolver already unwraps native binaries for supported npm layouts,
but can find another command shim in its Windows package-manager fallbacks
before checking for a native `codex.exe` on `PATH`. In the affected install,
that shim forwards to a Codex Desktop executable which is already on `PATH`.
Chat's app-server launch does not use the TUI hook overrides, so it can work
through the same shim without proving that TUI argument forwarding is sound.

## Decision

**Resolve a native Codex executable before accepting a fallback Windows batch
shim.** Keep the existing AO-managed and explicit npm install resolution order.
For the remaining Windows package-manager and `PATH` fallbacks, first inspect
native executables, skip the WindowsApps execution alias, and only then accept
`.cmd` or `.bat` shims. Preserve shim fallback when no native executable is
available so existing installations that only expose a supported launcher do
not become undiscoverable.

`ResolveCodexBinary` remains the single harness-level resolution boundary. Both
TUI command construction and Codex Chat continue to use its result; the TUI's
structured arguments therefore bypass `cmd.exe` when a native binary is
available. The generic process package and interactive `agent-process
supervise` behavior remain unchanged because they own unrelated executable
types and terminal inheritance semantics.

## Constraints and limits

**Do not add another quoting layer for the hook value.** Its quotes and
PowerShell call operator are meaningful Codex hook-command syntax, while
`cmd.exe` re-parsing occurs at a different boundary; extra escaping cannot
reliably preserve the TOML structure across both parsers.

**Do not treat WindowsApps' `codex.exe` alias as a native CLI.** It can be an
execution alias rather than a usable binary; selecting it ahead of a real
Codex install would replace the shim error with an unavailable-command error.

**An opaque shim remains a fallback only.** If no native executable can be
found and an unsupported `.cmd`/`.bat` launcher is the only installation,
structured TUI arguments may still be changed by that launcher. Safely
supporting more shim formats requires positively recognizing their interpreter,
entry point, and argument-forwarding behavior; do not execute or guess at
arbitrary batch-file contents to discover a target.

## Rejected alternatives

- Changing PowerShell hook quoting alone does not prevent `cmd.exe` from
  splitting the argument before Codex reads it.
- Rewriting the shared subprocess and terminal-launch layers would affect every
  Windows harness and require preserving each one’s console and signal
  behavior; Codex already has a dedicated binary resolver for this decision.
