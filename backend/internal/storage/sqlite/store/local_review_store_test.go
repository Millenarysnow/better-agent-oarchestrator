package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLocalDeliveryDecisionAndFeedbackAreDurable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	worker, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	submit := func(id string, at time.Time) {
		t.Helper()
		_, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: worker.ID, ProjectID: worker.ProjectID, DeliveryState: domain.ReportPending, State: domain.ReportDone, Note: "complete report", CreatedAt: at, AvailableAt: at, SettlementDeadline: at.Add(time.Minute), RepeatCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CreateLocalDelivery(ctx, domain.LocalDelivery{ID: id, SessionID: worker.ID, BaseSHA: "base", TargetSHA: "head", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	submit("delivery-1", now)
	decision, err := s.DecideLocalReview(ctx, worker.ID, "delivery-1", "changes_requested", "blocking defect", "", now)
	if err != nil || decision != "changes_requested" {
		t.Fatalf("return=%s err=%v", decision, err)
	}
	submit("delivery-2", now.Add(time.Second))
	decision, err = s.DecideLocalReview(ctx, worker.ID, "delivery-2", "changes_requested", "second blocker", "", now.Add(time.Second))
	if err != nil || decision != "needs_input" {
		t.Fatalf("second return=%s err=%v", decision, err)
	}
	if _, err := s.DecideLocalReview(ctx, worker.ID, "delivery-1", "accepted", "old", "", now); err == nil {
		t.Fatal("old delivery must not be accepted")
	}
	run := domain.ReviewRun{ID: "run-1", SessionID: worker.ID}
	for range 2 {
		if err := s.QueueLocalReviewResult(ctx, run, "review completed", now); err != nil {
			t.Fatal(err)
		}
	}
	reports, err := s.ListReportsBySession(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range reports {
		if r.ID == "local-review-run-1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("feedback delivered %d times", n)
	}
	facts, err := s.ListLocalReviewFactsForSessions(ctx, []domain.SessionID{worker.ID})
	if err != nil {
		t.Fatal(err)
	}
	if facts[worker.ID].ReworkCount != 1 || facts[worker.ID].Delivery.Decision != "needs_input" {
		t.Fatalf("facts=%+v", facts)
	}
	if err := s.UpsertReview(ctx, domain.Review{ID: "review", SessionID: worker.ID, ProjectID: worker.ProjectID, Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	pass := domain.ReviewRun{ID: "pass", ReviewID: "review", SessionID: worker.ID, Harness: domain.ReviewerCodex, TargetKind: "local", DeliveryID: "delivery-2", BaseSHA: "base", TargetSHA: "head", Status: domain.ReviewRunQueued, CreatedAt: now}
	if err := s.InsertReviewRun(ctx, pass); err != nil {
		t.Fatal(err)
	}
	duplicate := pass
	duplicate.ID, duplicate.Status = "duplicate", domain.ReviewRunLaunching
	if err := s.InsertReviewRun(ctx, duplicate); !errors.Is(err, domain.ErrDuplicateReviewRun) {
		t.Fatalf("launching pass beside a queued pass: %v", err)
	}
	if _, err := s.DecideLocalReview(ctx, worker.ID, "delivery-2", "needs_input", "needs eyes", "", now); err == nil {
		t.Fatal("recorded a decision while a local pass was queued")
	}
	if changed, err := s.TransitionReviewRun(ctx, pass.ID, []domain.ReviewRunStatus{domain.ReviewRunQueued}, domain.ReviewRunCompleted, domain.ReviewRunPatch{Verdict: domain.VerdictApproved}); err != nil || !changed {
		t.Fatalf("complete local review: changed=%v, err=%v", changed, err)
	}
	outcomes, err := s.UndeliveredLocalReviewRuns(ctx)
	if err != nil || len(outcomes) != 1 || outcomes[0].ID != pass.ID || outcomes[0].DeliveryID != pass.DeliveryID {
		t.Fatalf("completed local review missing from the outbox: %+v, err=%v", outcomes, err)
	}
	if _, err := s.DecideLocalReview(ctx, worker.ID, "delivery-2", "accepted", "User checked remaining evidence", "", now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"local-decision-delivery-2-needs_input", "local-decision-delivery-2-accepted"} {
		if _, found, err := s.GetReport(ctx, id); err != nil || !found {
			t.Fatalf("decision feedback %s: found=%v err=%v", id, found, err)
		}
	}
	pass.ID = "late-running"
	pass.Status = domain.ReviewRunRunning
	if err := s.InsertReviewRun(ctx, pass); err == nil {
		t.Fatal("review started after acceptance")
	}
}
