package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type nativeMessageAgent struct {
	fakeAgent
	submit func(context.Context, ports.TerminalMessageConfig) error
}

func (a nativeMessageAgent) SendTerminalMessage(ctx context.Context, cfg ports.TerminalMessageConfig) error {
	return a.submit(ctx, cfg)
}

func nativeMessageSession() domain.SessionRecord {
	return pastStartupGate(domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessOpenCodeV2, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "pty-1", RuntimeLaunchID: "launch-1",
			AgentSessionID: "ses_native", AgentSessionIDLaunchID: "launch-1",
			ConversationCheckpointState: domain.ConversationCheckpointComplete,
			LatestUserPrompt:            "human request",
		},
	})
}

func TestSendSemanticNativeTerminal(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = nativeMessageSession()
	msg := &fakeMessenger{}
	var received ports.TerminalMessageConfig
	agent := nativeMessageAgent{submit: func(_ context.Context, cfg ports.TerminalMessageConfig) error {
		received = cfg
		return nil
	}}
	m := newSendTestManager(t, agent, msg, st)
	m.dataDir = t.TempDir()
	if err := m.SendSemantic(context.Background(), "s1", "worker 已完成", "report-batch:done"); err != nil {
		t.Fatal(err)
	}
	if received.DataDir != m.dataDir || received.SessionID != "s1" || received.LaunchID != "launch-1" ||
		received.NativeSessionID != "ses_native" || received.DeliveryID != "report-batch:done" ||
		!strings.Contains(received.Message, "worker 已完成") {
		t.Fatalf("native delivery = %+v", received)
	}
	if id, ok := domain.ReportDeliveryID(received.Message); !ok || id != received.DeliveryID {
		t.Fatalf("report attribution missing: %q", received.Message)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("native report wrote %d messages to the terminal pane", len(msg.msgs))
	}
	if rec := st.sessions["s1"]; rec.Metadata.LatestUserPrompt != "human request" ||
		rec.Metadata.ConversationCheckpointState != domain.ConversationCheckpointComplete {
		t.Fatalf("internal report changed the human replay checkpoint: %+v", rec.Metadata)
	}

	nativeErr := errors.New("native report admission unconfirmed")
	agent.submit = func(context.Context, ports.TerminalMessageConfig) error { return nativeErr }
	m.agents = singleAgent{agent}
	if err := m.SendSemantic(context.Background(), "s1", "worker finished", "report-batch:retry"); !errors.Is(err, nativeErr) {
		t.Fatalf("native refusal = %v, want original error", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatal("failed native delivery fell back to unconfirmed pane input")
	}
}

func TestSendSemanticNativeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*domain.SessionRecord)
		want   error
	}{
		{"terminated", func(r *domain.SessionRecord) { r.IsTerminated = true }, ErrTerminated},
		{"exited", func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited }, ErrAgentExited},
		{"permission", func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityBlocked }, ErrAwaitingDecision},
		{"native_missing", func(r *domain.SessionRecord) { r.Metadata.AgentSessionID = "" }, ErrStartupPending},
		{"native_stale", func(r *domain.SessionRecord) { r.Metadata.AgentSessionIDLaunchID = "old" }, ErrStartupPending},
		{"runtime_missing", func(r *domain.SessionRecord) { r.Metadata.RuntimeHandleID = "" }, ErrStartupPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			rec := nativeMessageSession()
			tc.change(&rec)
			st.sessions["s1"] = rec
			calls := 0
			agent := nativeMessageAgent{submit: func(context.Context, ports.TerminalMessageConfig) error { calls++; return nil }}
			m := newSendTestManager(t, agent, &fakeMessenger{}, st)
			if err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:done"); !errors.Is(err, tc.want) {
				t.Fatalf("SendSemantic error = %v, want %v", err, tc.want)
			}
			if calls != 0 {
				t.Fatalf("unsafe session reached native admission %d time(s)", calls)
			}
		})
	}
}

type nativeMessageChangedStore struct {
	*fakeStore
	reads  int
	change func(*domain.SessionRecord)
}

func (s *nativeMessageChangedStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok, err := s.fakeStore.GetSession(ctx, id)
	s.reads++
	if s.reads > 1 {
		s.change(&r)
	}
	return r, ok, err
}

func TestSendSemanticNativeRechecksController(t *testing.T) {
	for _, change := range []struct {
		name string
		fn   func(*domain.SessionRecord)
	}{
		{"launch", func(r *domain.SessionRecord) { r.Metadata.RuntimeLaunchID = "replacement" }},
		{"harness", func(r *domain.SessionRecord) { r.Harness = domain.HarnessCodex }},
		{"interface", func(r *domain.SessionRecord) { r.Mode = domain.SessionModeChat }},
	} {
		t.Run(change.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s1"] = nativeMessageSession()
			calls := 0
			agent := nativeMessageAgent{submit: func(context.Context, ports.TerminalMessageConfig) error { calls++; return nil }}
			m := newSendTestManager(t, agent, &fakeMessenger{}, st)
			m.store = &nativeMessageChangedStore{fakeStore: st, change: change.fn}
			if err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:done"); !errors.Is(err, ErrOperationInProgress) {
				t.Fatalf("changed controller error = %v", err)
			}
			if calls != 0 {
				t.Fatal("a stale controller reached native admission")
			}
		})
	}
}

func TestSendSemanticNativeHoldsInputLease(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = nativeMessageSession()
	entered, release := make(chan struct{}), make(chan struct{})
	agent := nativeMessageAgent{submit: func(context.Context, ports.TerminalMessageConfig) error {
		close(entered)
		<-release
		return nil
	}}
	m := newSendTestManager(t, agent, &fakeMessenger{}, st)
	delivered := make(chan error, 1)
	go func() { delivered <- m.SendSemantic(context.Background(), "s1", "report", "report-batch:done") }()
	<-entered
	mutation := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { mutation <- m.beginAgentOperation(ctx, "s1", agentOperationKill) }()
	deadline := time.Now().Add(time.Second)
	for !m.SessionMutationInProgress("s1") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !m.SessionMutationInProgress("s1") {
		close(release)
		t.Fatal("kill did not close input admission")
	}
	select {
	case err := <-mutation:
		close(release)
		t.Fatalf("kill passed a still-active native delivery: %v", err)
	default:
	}
	if err := m.SendSemantic(ctx, "s1", "another report", "report-batch:next"); !errors.Is(err, ErrOperationInProgress) {
		close(release)
		t.Fatalf("kill did not reject a new native delivery: %v", err)
	}
	close(release)
	if err := <-delivered; err != nil {
		t.Fatal(err)
	}
	if err := <-mutation; err != nil {
		t.Fatal(err)
	}
	m.endAgentOperation("s1", agentOperationKill)
}
