package opencodev2

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTerminalReportsBridge(t *testing.T) {
	cfg := terminalReportConfig(t)
	stop := startReportPlugin(t, cfg, "ok")
	if err := New().SendTerminalMessage(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := New().SendTerminalMessage(context.Background(), cfg); err != nil {
		t.Fatalf("retry: %v", err)
	}
	admissions := readReportAdmissions(t, cfg)
	if len(admissions) != 1 {
		t.Fatalf("native admissions = %d, want one across retries", len(admissions))
	}
	if got := admissions[0]; got.ID != reportMessageID(cfg.NativeSessionID, cfg.DeliveryID) ||
		got.SessionID != cfg.NativeSessionID || got.Text != cfg.Message || got.Delivery != "queue" {
		t.Fatalf("native prompt = %+v", got)
	}
	stop()
	path, _ := reportBridgeLocation(cfg.DataDir, cfg.SessionID, cfg.LaunchID)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stopped plugin left its descriptor: %v", err)
	}

	// The provider journal survives a terminal relaunch; transport identity must
	// change without changing the native message identity used for deduplication.
	cfg.LaunchID = "launch-2"
	startReportPlugin(t, cfg, "ok")
	if err := New().SendTerminalMessage(context.Background(), cfg); err != nil {
		t.Fatalf("restarted terminal: %v", err)
	}
	if got := len(readReportAdmissions(t, cfg)); got != 1 {
		t.Fatalf("terminal restart created %d native admissions, want one", got)
	}
}

func TestTerminalReportLostReceipt(t *testing.T) {
	cfg := terminalReportConfig(t)
	startReportPlugin(t, cfg, "lost_receipt")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := New().SendTerminalMessage(ctx, cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost receipt error = %v, want deadline exceeded", err)
	}
	if got := len(readReportAdmissions(t, cfg)); got != 1 {
		t.Fatalf("native admission before lost receipt = %d, want one", got)
	}
	if err := New().SendTerminalMessage(context.Background(), cfg); err != nil {
		t.Fatalf("retry after lost receipt: %v", err)
	}
	if got := len(readReportAdmissions(t, cfg)); got != 1 {
		t.Fatalf("retry duplicated an already-admitted native prompt: %d", got)
	}
}

func TestTerminalReportRefusals(t *testing.T) {
	for _, mode := range []string{"failed", "wrong_id", "wrong_session", "wrong_type", "wrong_delivery", "old_version", "missing_api"} {
		t.Run(mode, func(t *testing.T) {
			cfg := terminalReportConfig(t)
			startReportPlugin(t, cfg, mode)
			if err := New().SendTerminalMessage(context.Background(), cfg); err == nil {
				t.Fatal("refused or mismatched native admission was accepted")
			}
		})
	}
}

func TestTerminalReportIdentity(t *testing.T) {
	cfg := terminalReportConfig(t)
	startReportPlugin(t, cfg, "ok")
	path, address := reportBridgeLocation(cfg.DataDir, cfg.SessionID, cfg.LaunchID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor reportBridgeDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"token", "session", "launch", "native", "message"} {
		t.Run(field, func(t *testing.T) {
			request := reportBridgeRequest{
				Version: 1, Token: descriptor.Token, SessionID: string(cfg.SessionID), LaunchID: cfg.LaunchID,
				NativeSessionID: cfg.NativeSessionID, DeliveryID: cfg.DeliveryID,
				MessageID: reportMessageID(cfg.NativeSessionID, cfg.DeliveryID), Text: cfg.Message,
			}
			switch field {
			case "token":
				request.Token = strings.Repeat("0", 64)
			case "session":
				request.SessionID = "other-worker"
			case "launch":
				request.LaunchID = "retired-launch"
			case "native":
				request.NativeSessionID = "ses_child"
			case "message":
				request.MessageID = "msg_other-batch"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := dialReportBridge(ctx, address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := json.NewEncoder(conn).Encode(request); err != nil {
				t.Fatal(err)
			}
			var reply reportBridgeReply
			if err := json.NewDecoder(conn).Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.Accepted || reply.Error == "" {
				t.Fatalf("wrong %s was accepted: %+v", field, reply)
			}
		})
	}
	if got := len(readReportAdmissions(t, cfg)); got != 0 {
		t.Fatalf("invalid requests reached native admission %d time(s)", got)
	}

	// The current root stays the target even though child-created and child
	// execution events were emitted by the same provider event stream.
	if err := New().SendTerminalMessage(context.Background(), cfg); err != nil {
		t.Fatalf("child traffic displaced the root: %v", err)
	}
}

func TestTerminalReportDescriptor(t *testing.T) {
	cfg := terminalReportConfig(t)
	if err := New().SendTerminalMessage(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "resume the terminal") {
		t.Fatalf("missing bridge error = %v", err)
	}
	stop := startReportPlugin(t, cfg, "ok")
	path, _ := reportBridgeLocation(cfg.DataDir, cfg.SessionID, cfg.LaunchID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor reportBridgeDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.Address = "127.0.0.1:1234"
	changed, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().SendTerminalMessage(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "current controller") {
		t.Fatalf("foreign endpoint error = %v", err)
	}
	descriptor.Token = strings.Repeat("f", 64)
	changed, err = json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("old plugin removed a replacement descriptor: %v", err)
	}
}

type reportAdmission struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Text      string `json:"text"`
	Delivery  string `json:"delivery"`
}

func terminalReportConfig(t *testing.T) ports.TerminalMessageConfig {
	t.Helper()
	return ports.TerminalMessageConfig{
		DataDir: t.TempDir(), SessionID: "ao-orchestrator", LaunchID: "launch-1", NativeSessionID: "ses_parent",
		DeliveryID: "report-batch:worker-done", Message: "Reports since your previous turn:\n[done] worker 已完成，等待审查。",
	}
}

func readReportAdmissions(t *testing.T, cfg ports.TerminalMessageConfig) []reportAdmission {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg.DataDir, "admissions.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var admissions []reportAdmission
	if err := json.Unmarshal(data, &admissions); err != nil {
		t.Fatal(err)
	}
	return admissions
}

func startReportPlugin(t *testing.T, cfg ports.TerminalMessageConfig, mode string) func() {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the native report bridge fixture")
	}
	fixture := t.TempDir()
	module := filepath.Join(fixture, "plugin.mjs")
	harness := filepath.Join(fixture, "harness.mjs")
	writeV2TestFile(t, module, v2PluginSource, 0o600)
	writeV2TestFile(t, harness, reportPluginHarness, 0o600)
	cmd := exec.Command(node, harness, module, cfg.DataDir, mode)
	cmd.Dir = fixture
	env := map[string]string{}
	New().AugmentRuntimeLaunchEnv(env, cfg.DataDir, cfg.SessionID, cfg.LaunchID)
	cmd.Env = append(envWithoutPath(os.Environ()), "PATH="+fixture, "AO_SESSION_ID="+string(cfg.SessionID), "AO_RUNTIME_LAUNCH_ID="+cfg.LaunchID)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop := func() {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		_, _ = io.WriteString(stdin, "stop\n")
		_ = stdin.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("report plugin: %v\n%s", err, stderr.String())
			}
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("report plugin did not stop")
		}
	}
	t.Cleanup(stop)
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ready:
		if line != "ready" {
			t.Fatalf("report plugin failed to start: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("report plugin startup timed out")
	}
	return stop
}

const reportPluginHarness = `
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const [modulePath, directory, mode] = process.argv.slice(2);
const plugin = (await import(pathToFileURL(modulePath).href)).default;
const journal = join(directory, "admissions.json");
let saved = [];
try { saved = JSON.parse(await readFile(journal, "utf8")); } catch {}
const context = {
  app: { version: mode === "old_version" ? "2.0.19" : "2.3.0" },
  location: { directory },
  event: { subscribe({ signal }) { return { async *[Symbol.asyncIterator]() {
    yield { type: "session.created", data: { sessionID: "ses_parent" } };
    yield { type: "session.created", data: { sessionID: "ses_child", parentID: "ses_parent" } };
    yield { type: "session.execution.started", data: { sessionID: "ses_child" } };
    if (!signal.aborted) await new Promise(resolve => signal.addEventListener("abort", resolve, { once: true }));
  } }; } },
  session: { async prompt(input, { signal }) {
    if (mode === "failed") throw new Error("native admission refused");
    let entry = saved.find(value => value.id === input.id);
    if (!entry) {
      entry = input;
      saved.push(entry);
      await writeFile(journal, JSON.stringify(saved));
      if (mode === "lost_receipt") await new Promise((resolve, reject) => {
        const fail = () => reject(new Error("receipt lost"));
        if (signal.aborted) fail(); else signal.addEventListener("abort", fail, { once: true });
      });
    }
    return {
      id: mode === "wrong_id" ? "msg_wrong" : entry.id,
      sessionID: mode === "wrong_session" ? "ses_other" : entry.sessionID,
      type: mode === "wrong_type" ? "synthetic" : "user",
      delivery: mode === "wrong_delivery" ? "steer" : entry.delivery,
    };
  } },
  tool: { async hook() { return { async dispose() {} }; } },
};
if (mode === "missing_api") delete context.session.prompt;
const cleanup = await plugin.setup(context);
console.log("ready");
await new Promise(resolve => process.stdin.once("data", resolve));
await cleanup();
`
