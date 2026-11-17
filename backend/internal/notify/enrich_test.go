package notify

import (
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestEnrichLocalReviewWithoutPR(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		typ   domain.NotificationType
		title string
		body  string
	}{
		{domain.NotificationReviewCompleted, "Local review complete for Checkout flow", "The local delivery review passed. Explicit acceptance is still required."},
		{domain.NotificationReviewChangesRequested, "Local review found changes for Checkout flow", "The local delivery review found changes to address. The coordinator or user decides whether to return it."},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			rec, err := enrich(Intent{
				Type: tc.typ, SessionID: "sess-1", ProjectID: "proj-1",
				ReviewTargetKind: "local", SessionDisplayName: "Checkout flow",
				CreatedAt: time.Now(), SourceKey: "review_run:run-1",
			})
			if err != nil {
				t.Fatal(err)
			}
			if rec.PRURL != "" || rec.Title != tc.title || rec.Body != tc.body || rec.SourceKey != "review_run:run-1" {
				t.Fatalf("local review notification = %+v", rec)
			}
		})
	}
}

func TestEnrichMissingPRStillRejectsPRNotifications(t *testing.T) {
	t.Parallel()
	for _, intent := range []Intent{
		{Type: domain.NotificationReviewCompleted},
		{Type: domain.NotificationReviewChangesRequested},
		{Type: domain.NotificationReadyToMerge, ReviewTargetKind: "local"},
		{Type: domain.NotificationPRMerged, ReviewTargetKind: "local"},
	} {
		intent.SessionID, intent.CreatedAt = "sess-1", time.Now()
		if _, err := enrich(intent); !errors.Is(err, domain.ErrInvalidNotificationRecord) {
			t.Fatalf("missing PR accepted for %+v: %v", intent, err)
		}
	}
}

func TestEnrichReadyToMergePrioritizesPRContext(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationReadyToMerge,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		PRURL:              "https://github.com/acme/app/pull/67",
		SessionDisplayName: "Checkout flow",
		PRNumber:           67,
		PRTitle:            "Fix checkout totals",
		CreatedAt:          time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "Fix checkout totals · PR #67"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
}

func TestEnrichReadyToMergeFallsBackWithoutPRTitle(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationReadyToMerge,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		PRURL:     "https://github.com/acme/app/pull/67",
		PRNumber:  67,
		CreatedAt: time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "PR #67 is ready to merge"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
}
