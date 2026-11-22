package controllers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

type fakeLocalReviewService struct {
	fakeReviewService
	delivery, decision string
	actor              domain.SessionID
	enabled            bool
	err                error
}

func (f *fakeLocalReviewService) Local(context.Context, domain.SessionID) (*domain.LocalReviewSummary, error) {
	return &domain.LocalReviewSummary{Delivery: domain.LocalDelivery{ID: "report-1"}}, f.err
}
func (f *fakeLocalReviewService) DecideLocal(_ context.Context, _ domain.SessionID, id, decision, _ string, actor domain.SessionID) (string, error) {
	f.delivery, f.decision, f.actor = id, decision, actor
	return decision, f.err
}
func (f *fakeLocalReviewService) SetLocalAuto(_ context.Context, _ domain.SessionID, enabled bool) error {
	f.enabled = enabled
	return f.err
}

func TestLocalReviewHTTPBoundary(t *testing.T) {
	f := &fakeLocalReviewService{}
	srv := newReviewTestServer(t, f)
	_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/worker-1/reviews/trigger", `{"local":true}`)
	if status != http.StatusCreated || !f.triggerRequest.Local {
		t.Fatalf("trigger=%d %+v", status, f.triggerRequest)
	}
	_, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/worker-1/reviews/local/decision", `{"deliveryId":"report-1","decision":"accepted","actorSessionId":"orchestrator-1"}`)
	if status != http.StatusOK || f.delivery != "report-1" || f.actor != "orchestrator-1" {
		t.Fatalf("decision=%d %+v", status, f)
	}
	_, status, _ = doRequest(t, srv, "PUT", "/api/v1/sessions/worker-1/reviews/local/auto", `{}`)
	if status != http.StatusBadRequest {
		t.Fatalf("missing bool accepted: %d", status)
	}
	f.err = reviewsvc.ErrConflict
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/worker-1/reviews/local/decision", `{"deliveryId":"old","decision":"accepted"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "REVIEW_CONFLICT")
}
