package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func (s *Service) localReviewFacts(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.LocalReviewSummary, error) {
	if reader, ok := s.store.(interface {
		ListLocalReviewFactsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID]domain.LocalReviewSummary, error)
	}); ok {
		return reader.ListLocalReviewFactsForSessions(ctx, ids)
	}
	return nil, nil
}

func (s *Service) applyLocalReview(sess *domain.Session, facts map[domain.SessionID]domain.LocalReviewSummary) {
	x, ok := facts[sess.ID]
	if !ok {
		return
	}
	x.Stale = (sess.BranchState != nil && (sess.BranchState.WorktreeDirty || (sess.BranchState.HeadSHA != "" && sess.BranchState.HeadSHA != x.Delivery.TargetSHA))) || domain.LocalDeliveryHasNewDirection(sess.SessionRecord, x.Delivery)
	sess.LocalReview = &x
	if len(sess.PRs) > 0 {
		return
	}
	sf := toContractKanbanSessionFacts(sess.SessionRecord, false, s.harnessSignals(sess.Harness))
	sf.LocalDelivery = &contract.LocalKanbanFacts{Stale: x.Stale, Invalid: x.Delivery.InputError != "", Decision: x.Delivery.Decision, ReviewStatus: string(x.Status), Verdict: string(x.Verdict)}
	p := contract.DeriveKanbanPresentation(sf, nil, s.now(), noSignalGrace)
	sess.KanbanColumn = p.Column
	sess.DisplayStatus = p.DisplayStatus
}
