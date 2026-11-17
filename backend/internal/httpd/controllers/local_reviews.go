package controllers

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

type localReviewService interface {
	Local(context.Context, domain.SessionID) (*domain.LocalReviewSummary, error)
	DecideLocal(context.Context, domain.SessionID, string, string, string, domain.SessionID) (string, error)
	SetLocalAuto(context.Context, domain.SessionID, bool) error
}

func (c *ReviewsController) local(w http.ResponseWriter, r *http.Request) {
	s, ok := c.Svc.(localReviewService)
	if !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/reviews/local")
		return
	}
	x, err := s.Local(r.Context(), sessionID(r))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LocalReviewResponse{LocalReview: x})
}

func (c *ReviewsController) localDecision(w http.ResponseWriter, r *http.Request) {
	s, ok := c.Svc.(localReviewService)
	if !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/local/decision")
		return
	}
	var in LocalReviewDecisionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	decision, err := s.DecideLocal(r.Context(), sessionID(r), in.DeliveryID, in.Decision, in.Note, domain.SessionID(in.ActorSessionID))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LocalReviewDecisionResponse{Decision: decision})
}

func (c *ReviewsController) localAuto(w http.ResponseWriter, r *http.Request) {
	s, ok := c.Svc.(localReviewService)
	if !ok {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/reviews/local/auto")
		return
	}
	var in LocalReviewAutoRequest
	if err := decodeJSON(r, &in); err != nil || in.Enabled == nil {
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_JSON", "An enabled boolean is required", nil)
		return
	}
	if err := s.SetLocalAuto(r.Context(), sessionID(r), *in.Enabled); err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LocalReviewAutoResponse{Enabled: *in.Enabled})
}
