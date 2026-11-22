package review

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
)

type localStore interface {
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	CreateLocalDelivery(context.Context, domain.LocalDelivery) error
	UndeliveredLocalReviewRuns(context.Context) ([]domain.ReviewRun, error)
	GetLocalDelivery(context.Context, domain.SessionID) (domain.LocalDelivery, bool, error)
	ListLocalReviewFactsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID]domain.LocalReviewSummary, error)
	DecideLocalReview(context.Context, domain.SessionID, string, string, string, domain.SessionID, time.Time) (string, error)
	SetSessionAutoLocalReview(context.Context, domain.SessionID, bool, time.Time) (bool, error)
	QueueLocalReviewResult(context.Context, domain.ReviewRun, string, time.Time) error
}

func localGit(ctx context.Context, root string, args ...string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: local workspace path is unavailable", ErrInvalid)
	}
	args = append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", root}, args...)
	out, err := aoprocess.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("%w: unable to read local Git version (%s)", ErrInvalid, args[len(args)-1])
	}
	return strings.TrimSpace(string(out)), nil
}

// PrepareLocalCompletion pins the version before the report and delivery are
// committed atomically. Invalid input is retained; triggers never recapture HEAD.
func (s *Service) PrepareLocalCompletion(ctx context.Context, report domain.ReportRecord) (*domain.LocalDelivery, error) {
	if report.State != domain.ReportDone {
		return nil, nil
	}
	for _, output := range report.Outputs {
		if output.Kind == domain.ReportOutputPRCreated {
			return nil, nil
		}
	}
	st, ok := s.store.(localStore)
	if !ok {
		return nil, nil
	}
	worker, found, err := s.store.GetSession(ctx, report.SessionID)
	if err != nil {
		return nil, err
	}
	if !found || worker.Kind != domain.KindWorker || worker.IsTerminated {
		return nil, nil
	}
	prs, err := s.store.ListPRsBySession(ctx, worker.ID)
	if err != nil {
		return nil, err
	}
	if len(prs) > 0 {
		return nil, nil
	}
	project, found, err := st.GetProject(ctx, string(worker.ProjectID))
	if err != nil {
		return nil, err
	}
	if !found || project.Kind.WithDefault() != domain.ProjectKindSingleRepo {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := domain.LocalDelivery{ID: report.ID, SessionID: worker.ID, TargetBranch: worker.Metadata.DiffBaseRef, CreatedAt: report.CreatedAt}
	if d.TargetBranch == "" {
		d.TargetBranch = project.Config.DefaultBranch
	}
	d.TargetSHA, err = localGit(ctx, worker.Metadata.WorkspacePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err == nil {
		if worker.Metadata.DiffBaseSHA == "" {
			err = fmt.Errorf("%w: local delivery has no recorded comparison base", ErrInvalid)
		} else {
			d.BaseSHA, err = localGit(ctx, worker.Metadata.WorkspacePath, "rev-parse", "--verify", worker.Metadata.DiffBaseSHA+"^{commit}")
		}
	}
	if err == nil {
		var dirty string
		dirty, err = localGit(ctx, worker.Metadata.WorkspacePath, "status", "--porcelain", "--untracked-files=normal")
		if err == nil && dirty != "" {
			err = fmt.Errorf("%w: local delivery contains uncommitted files; commit the intended changes and send a new done report", ErrInvalid)
		}
	}
	if err != nil {
		d.InputError = err.Error()
	}
	if d.InputError == "" {
		head, err := localGit(ctx, worker.Metadata.WorkspacePath, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || head != d.TargetSHA {
			d.InputError = "The local version changed while submitting; send a new complete done report"
		}
	}
	return &d, nil
}

// Local returns the current delivery, review consensus and live Git validity.
func (s *Service) Local(ctx context.Context, id domain.SessionID) (*domain.LocalReviewSummary, error) {
	st, ok := s.store.(localStore)
	if !ok {
		return nil, nil
	}
	facts, err := st.ListLocalReviewFactsForSessions(ctx, []domain.SessionID{id})
	if err != nil {
		return nil, err
	}
	x, found := facts[id]
	if !found {
		return nil, nil
	}
	worker, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if found {
		head, e := localGit(ctx, worker.Metadata.WorkspacePath, "rev-parse", "--verify", "HEAD^{commit}")
		x.Stale = e != nil || head != x.Delivery.TargetSHA || domain.LocalDeliveryHasNewDirection(worker, x.Delivery)
		if !x.Stale {
			dirty, err := localGit(ctx, worker.Metadata.WorkspacePath, "status", "--porcelain", "--untracked-files=normal")
			x.Stale = err != nil || dirty != ""
		}
	}
	return &x, nil
}

func (s *Service) triggerLocal(ctx context.Context, id domain.SessionID, req TriggerRequest) (TriggerOutcome, error) {
	if req.PRURL != "" {
		return TriggerOutcome{}, fmt.Errorf("%w: local review cannot name a PR", ErrInvalid)
	}
	x, err := s.Local(ctx, id)
	if err != nil {
		return TriggerOutcome{}, err
	}
	if x == nil {
		return TriggerOutcome{}, fmt.Errorf("%w: submit a new done report before requesting local review", ErrInvalid)
	}
	if x.Stale || x.Delivery.InputError != "" {
		return TriggerOutcome{}, fmt.Errorf("%w: local delivery is stale or invalid: %s; submit a new done report", ErrInvalid, x.Delivery.InputError)
	}
	if x.Delivery.Decision != "" && x.Delivery.Decision != "needs_input" {
		return TriggerOutcome{}, fmt.Errorf("%w: this delivery already has an acceptance decision", ErrInvalid)
	}
	worker, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return TriggerOutcome{}, err
	}
	if !ok {
		return TriggerOutcome{}, ErrNotFound
	}
	dirty, err := localGit(ctx, worker.Metadata.WorkspacePath, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return TriggerOutcome{}, err
	}
	if dirty != "" {
		return TriggerOutcome{}, fmt.Errorf("%w: commit local changes and send a new done report", ErrInvalid)
	}
	if reader, ok := s.store.(interface {
		GetReport(context.Context, string) (domain.ReportRecord, bool, error)
	}); ok {
		r, found, e := reader.GetReport(ctx, x.Delivery.ID)
		if e != nil {
			return TriggerOutcome{}, e
		}
		if found {
			x.Delivery.CompletionReport = r.Note + "\n" + r.Message
			for _, o := range r.Outputs {
				x.Delivery.CompletionReport += "\n" + string(o.Kind) + ": " + o.Reference
			}
		}
	}
	result, err := s.triggerWithOptions(ctx, id, reviewcore.TriggerOptions{Harness: req.Harness, Config: req.Config, Source: req.Source, InterfaceMode: req.InterfaceMode, LocalTarget: &x.Delivery, Rerun: req.Rerun, RejectReviewedHead: req.RejectReviewedHead})
	return TriggerOutcome{TriggerResult: result}, err
}

// TriggerLocalAuto reviews a current, unreviewed completion when automation is due.
func (s *Service) TriggerLocalAuto(ctx context.Context, id domain.SessionID) (reviewcore.TriggerResult, error) {
	x, err := s.Local(ctx, id)
	if err != nil || x == nil || x.Stale || x.Delivery.InputError != "" || x.Delivery.Decision != "" || x.Status != "" {
		return reviewcore.TriggerResult{SkipReason: "local_delivery_not_due"}, err
	}
	result, err := s.triggerLocal(ctx, id, TriggerRequest{Local: true, Source: domain.ReviewTriggerAuto})
	return result.TriggerResult, err
}

// SetLocalAuto changes only this worker's local automatic review preference.
func (s *Service) SetLocalAuto(ctx context.Context, id domain.SessionID, enabled bool) error {
	st, ok := s.store.(localStore)
	if !ok {
		return fmt.Errorf("%w: local review is unavailable", ErrInvalid)
	}
	worker, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if worker.Kind != domain.KindWorker || worker.IsTerminated {
		return fmt.Errorf("%w: local automation requires a live worker", ErrInvalid)
	}
	changed, err := st.SetSessionAutoLocalReview(ctx, id, enabled, s.clock())
	if err == nil && !changed {
		return ErrNotFound
	}
	return err
}

// DecideLocal validates the actor and pinned version before recording acceptance.
func (s *Service) DecideLocal(ctx context.Context, id domain.SessionID, deliveryID, decision, note string, actor domain.SessionID) (string, error) {
	if decision != "accepted" && decision != "changes_requested" && decision != "needs_input" {
		return "", fmt.Errorf("%w: unknown local acceptance decision", ErrInvalid)
	}
	if len([]rune(note)) > 800 {
		return "", fmt.Errorf("%w: decision note must be at most 800 characters", ErrInvalid)
	}
	worker, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok || worker.IsTerminated {
		return "", fmt.Errorf("%w: worker is unavailable", ErrInvalid)
	}
	if actor == id {
		return "", fmt.Errorf("%w: a worker cannot accept or return its own delivery", ErrInvalid)
	}
	if actor != "" {
		a, found, e := s.store.GetSession(ctx, actor)
		if e != nil {
			return "", e
		}
		if !found || a.Kind != domain.KindOrchestrator || a.ProjectID != worker.ProjectID || a.IsTerminated {
			return "", fmt.Errorf("%w: decision requires the project's active orchestrator or the user", ErrInvalid)
		}
	}
	x, err := s.Local(ctx, id)
	if err != nil {
		return "", err
	}
	if x == nil || x.Stale || x.Delivery.ID != deliveryID || x.Delivery.InputError != "" {
		return "", fmt.Errorf("%w: acceptance must target the current valid delivery", ErrConflict)
	}
	if x.Status.IsActive() {
		return "", fmt.Errorf("%w: wait for the running review", ErrConflict)
	}
	if decision == "changes_requested" && (x.Status != domain.ReviewRunCompleted || x.Verdict != domain.VerdictChangesRequested) {
		return "", fmt.Errorf("%w: return requires a completed review with blocking findings", ErrInvalid)
	}
	if decision == "accepted" && (x.Status != domain.ReviewRunCompleted || x.Verdict != domain.VerdictApproved) {
		return "", fmt.Errorf("%w: acceptance requires a completed approved review", ErrInvalid)
	}
	dirty, err := localGit(ctx, worker.Metadata.WorkspacePath, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "", err
	}
	if dirty != "" {
		return "", fmt.Errorf("%w: uncommitted changes invalidate acceptance", ErrConflict)
	}
	if decision != "accepted" && strings.TrimSpace(note) == "" {
		return "", fmt.Errorf("%w: a return or input decision requires a reason", ErrInvalid)
	}
	st, supported := s.store.(localStore)
	if !supported {
		return "", fmt.Errorf("%w: local review is unavailable", ErrInvalid)
	}
	result, err := st.DecideLocalReview(ctx, id, deliveryID, decision, note, actor, s.clock())
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrConflict, err)
	}
	if s.localWake != nil {
		s.localWake()
	}
	return result, nil
}

func (s *Service) queueLocalResult(ctx context.Context, run domain.ReviewRun) error {
	st, ok := s.store.(localStore)
	if !ok {
		return nil
	}
	current, found, err := st.GetLocalDelivery(ctx, run.SessionID)
	if err != nil {
		return err
	}
	historical := !found || current.ID != run.DeliveryID
	note := fmt.Sprintf("Local reviewer %s completed delivery %s at commit %s: %s. Full findings: ao review ls %s --json. Coordinator: inspect the report, distinguish blocking findings from suggestions, then use ao review decide with this delivery id. Send blocking rework to the worker only after recording the return; at most one rework round. An approval does not authorize merge or push.", run.Harness, run.DeliveryID, run.TargetSHA, run.Verdict, run.SessionID)
	if historical {
		note = fmt.Sprintf("Historical local review %s for superseded delivery %s: %s/%s. Inspect with ao review ls %s --json. Do not apply this outcome to the current delivery.", run.ID, run.DeliveryID, run.Status, run.Verdict, run.SessionID)
	}
	if err := st.QueueLocalReviewResult(ctx, run, note, s.clock()); err != nil {
		return err
	}
	if s.localWake != nil {
		s.localWake()
	}
	return nil
}

// WithLocalReviewWake connects the durable result outbox to report delivery.
func (s *Service) WithLocalReviewWake(wake func()) { s.localWake = wake }

// ReconcileLocalFeedback repairs the result/outbox boundary after a daemon crash,
// even when local automatic review is disabled.
func (s *Service) ReconcileLocalFeedback(ctx context.Context) error {
	st, ok := s.store.(localStore)
	if !ok {
		return nil
	}
	runs, err := st.UndeliveredLocalReviewRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if err := s.queueLocalResult(ctx, run); err != nil {
			return err
		}
	}
	return nil
}
