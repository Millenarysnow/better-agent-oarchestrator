package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestLocalCompletionPinsVersionAndRejectsStaleAcceptance(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Local Review Test")
	git("config", "user.email", "local-review@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "code.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "code.txt"), []byte("implemented"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "implementation")
	head := git("rev-parse", "HEAD")
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "project", Path: root, RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	worker, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "project", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now, Metadata: domain.SessionMetadata{WorkspacePath: root, DiffBaseSHA: base, DiffBaseRef: "refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(nil, store)
	reports := reportsvc.New(reportsvc.Deps{Store: store, Completion: svc.PrepareLocalCompletion, Now: func() time.Time { return now }, NewID: func() string { return "done-1" }})
	report, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: worker.ID, State: domain.ReportDone, Note: "complete, tested"})
	if err != nil {
		t.Fatal(err)
	}
	local, err := svc.Local(ctx, worker.ID)
	if err != nil || local == nil || local.Delivery.TargetSHA != head || local.Delivery.BaseSHA != base || local.Stale {
		t.Fatalf("delivery=%+v err=%v", local, err)
	}
	if _, err := svc.DecideLocal(ctx, worker.ID, report.ID, "accepted", "", worker.ID); err == nil {
		t.Fatal("worker accepted its own delivery")
	}
	if _, err := svc.DecideLocal(ctx, worker.ID, report.ID, "accepted", "", ""); err == nil {
		t.Fatal("delivery accepted before independent review")
	}
	if err := store.UpsertReview(ctx, domain.Review{ID: "review-1", SessionID: worker.ID, ProjectID: worker.ProjectID, Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run := domain.ReviewRun{ID: "run-1", ReviewID: "review-1", SessionID: worker.ID, Harness: domain.ReviewerCodex, TargetKind: "local", DeliveryID: report.ID, BaseSHA: base, TargetSHA: head, Status: domain.ReviewRunRunning, CreatedAt: now}
	if err := store.InsertReviewRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, worker.ID, run.ID, domain.VerdictApproved, "No blocking findings; editor smoke test remains manual", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideLocal(ctx, worker.ID, report.ID, "accepted", "evidence checked", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "code.txt"), []byte("new version"), 0600); err != nil {
		t.Fatal(err)
	}
	local, err = svc.Local(ctx, worker.ID)
	if err != nil || !local.Stale {
		t.Fatalf("dirty delivery remained current: %+v %v", local, err)
	}
	git("add", ".")
	git("commit", "-m", "new version")
	local, err = svc.Local(ctx, worker.ID)
	if err != nil || !local.Stale {
		t.Fatalf("new version did not stale delivery: %+v %v", local, err)
	}
	if _, err := svc.DecideLocal(ctx, worker.ID, report.ID, "accepted", "", ""); err == nil {
		t.Fatal("old acceptance applied to new HEAD")
	}
}
