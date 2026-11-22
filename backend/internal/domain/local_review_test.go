package domain

import (
	"testing"
	"time"
)

func TestCurrentLocalReviewRequiresAllLatestOpinions(t *testing.T) {
	now := time.Now()
	approved := ReviewRun{ID: "a", TargetKind: "local", DeliveryID: "d", Harness: ReviewerCodex, Status: ReviewRunCompleted, Verdict: VerdictApproved, CreatedAt: now}
	for _, status := range append(append([]ReviewRunStatus{}, ActiveReviewRunStatuses...), ReviewRunFailed, ReviewRunCancelled, ReviewRunCompleted) {
		other := approved
		other.ID, other.Harness, other.Status, other.Verdict = "b", ReviewerClaudeCode, status, VerdictNone
		if status == ReviewRunCompleted {
			other.Verdict = VerdictChangesRequested
		}
		got, _ := CurrentLocalReviewRun("d", []ReviewRun{other, approved})
		if got.ID != "b" {
			t.Fatalf("%s opinion hidden by approval: %+v", status, got)
		}
		newer := other
		newer.ID, newer.CreatedAt, newer.Status, newer.Verdict = "c", now.Add(time.Second), ReviewRunCompleted, VerdictApproved
		got, _ = CurrentLocalReviewRun("d", []ReviewRun{other, approved, newer})
		if got.Status != ReviewRunCompleted || got.Verdict != VerdictApproved {
			t.Fatalf("replaced opinion still blocks: %+v", got)
		}
	}
}
