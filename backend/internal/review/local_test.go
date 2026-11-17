package review

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLocalReviewLaunchesWithoutPRAndPinsVersions(t *testing.T) {
	worker := liveWorker()
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "local-review"}
	engine := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, fakePRs{}, fakeProjects{}, launcher)
	d := domain.LocalDelivery{ID: "report-1", SessionID: worker.ID, BaseSHA: strings.Repeat("a", 40), TargetSHA: strings.Repeat("b", 40)}
	result, err := engine.TriggerWithOptions(context.Background(), worker.ID, TriggerOptions{LocalTarget: &d})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || !launcher.spawned || result.Run.TargetKind != "local" || result.Run.DeliveryID != d.ID || launcher.gotSpec.BaseSHA != d.BaseSHA || launcher.gotSpec.PRURL != "" {
		t.Fatalf("result=%+v spec=%+v", result, launcher.gotSpec)
	}
	prompt, system := reviewTexts(launcher.gotSpec)
	if strings.Contains(prompt, "gh api") || !strings.Contains(prompt, d.BaseSHA) || !strings.Contains(prompt, d.TargetSHA) || !strings.Contains(system, "must not edit") {
		t.Fatal("local review must pin both revisions and submit without a provider")
	}
}

func TestLocalPlanDoesNotReuseAnotherDelivery(t *testing.T) {
	d := domain.LocalDelivery{ID: "new", TargetSHA: "same-sha"}
	states := planTargets(nil, &d, []domain.ReviewRun{{ID: "old", TargetKind: "local", DeliveryID: "old", TargetSHA: "same-sha", Verdict: domain.VerdictApproved}})
	if len(states) != 1 || states[0].Status != ReviewStateNeedsReview || states[0].LatestRun != nil {
		t.Fatalf("stale delivery influenced plan: %+v", states)
	}
}

func TestLocalReviewDoesNotCancelAnotherTarget(t *testing.T) {
	worker := liveWorker()
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "pr-run", TargetKind: "pr", Status: domain.ReviewRunRunning}}}
	launcher := &fakeLauncher{handle: "local-review"}
	engine := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, fakePRs{}, fakeProjects{}, launcher)
	d := domain.LocalDelivery{ID: "done", SessionID: worker.ID, BaseSHA: "base", TargetSHA: "head"}
	if _, err := engine.TriggerWithOptions(context.Background(), worker.ID, TriggerOptions{LocalTarget: &d}); err == nil {
		t.Fatal("overlapped a local task with an active PR review")
	}
	if store.runs[0].Status != domain.ReviewRunRunning || launcher.spawned {
		t.Fatal("existing review was replaced")
	}
}
