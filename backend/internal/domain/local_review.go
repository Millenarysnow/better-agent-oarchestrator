package domain

import "time"

// LocalDelivery identifies the immutable version submitted by a worker's done
// report. Decisions belong to this version, never to a moving branch.
type LocalDelivery struct {
	CompletionReport string     `json:"-"`
	ID               string     `json:"id"`
	SessionID        SessionID  `json:"sessionId"`
	BaseSHA          string     `json:"baseSha"`
	TargetSHA        string     `json:"targetSha"`
	TargetBranch     string     `json:"targetBranch"`
	InputError       string     `json:"inputError,omitempty"`
	Decision         string     `json:"decision,omitempty" enum:",accepted,changes_requested,needs_input"`
	DecisionNote     string     `json:"decisionNote,omitempty"`
	DecidedBy        SessionID  `json:"decidedBy,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	DecidedAt        *time.Time `json:"decidedAt,omitempty"`
}

// LocalReviewSummary is a read model. Its stage is derived, never persisted.
type LocalReviewSummary struct {
	Delivery    LocalDelivery   `json:"delivery"`
	Status      ReviewRunStatus `json:"status,omitempty"`
	Verdict     ReviewVerdict   `json:"verdict,omitempty"`
	RunID       string          `json:"runId,omitempty"`
	ReworkCount int64           `json:"reworkCount"`
	Stale       bool            `json:"stale"`
}

// LocalDeliveryHasNewDirection excludes SCM/preference updates and internal
// reviewer bookkeeping. Both human and coordinator task direction invalidate it.
func LocalDeliveryHasNewDirection(worker SessionRecord, d LocalDelivery) bool {
	return worker.Metadata.LatestUserPromptAt.After(d.CreatedAt) || worker.Metadata.LatestInteractionAt.After(d.CreatedAt)
}

// CurrentLocalReviewRun combines each harness's latest opinion. Any running,
// blocking or unsuccessful opinion prevents another harness's approval from
// making the whole delivery appear approved.
func CurrentLocalReviewRun(deliveryID string, runs []ReviewRun) (ReviewRun, bool) {
	latest := map[ReviewerHarness]ReviewRun{}
	for _, run := range runs {
		if run.TargetKind != "local" || run.DeliveryID != deliveryID {
			continue
		}
		old, ok := latest[run.Harness]
		if !ok || run.CreatedAt.After(old.CreatedAt) || (run.CreatedAt.Equal(old.CreatedAt) && run.ID > old.ID) {
			latest[run.Harness] = run
		}
	}
	priority := func(r ReviewRun) int {
		switch {
		case r.Status == ReviewRunRunning:
			return 5
		case r.Status == ReviewRunComplete && r.Verdict == VerdictChangesRequested:
			return 4
		case r.Status == ReviewRunFailed:
			return 3
		case r.Status == ReviewRunCancelled:
			return 2
		case r.Status == ReviewRunComplete && r.Verdict == VerdictApproved:
			return 1
		default:
			return 3
		}
	}
	var result ReviewRun
	found := false
	for _, run := range latest {
		if !found || priority(run) > priority(result) || (priority(run) == priority(result) && (run.CreatedAt.After(result.CreatedAt) || (run.CreatedAt.Equal(result.CreatedAt) && run.ID > result.ID))) {
			result, found = run, true
		}
	}
	return result, found
}
