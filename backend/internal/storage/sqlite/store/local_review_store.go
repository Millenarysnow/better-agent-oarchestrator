package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func (s *Store) CreateLocalDelivery(ctx context.Context, d domain.LocalDelivery) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.InsertLocalDelivery(ctx, localDeliveryParams(d))
}

func localDeliveryParams(d domain.LocalDelivery) gen.InsertLocalDeliveryParams {
	return gen.InsertLocalDeliveryParams{ID: d.ID, SessionID: string(d.SessionID), BaseSha: d.BaseSHA, TargetSha: d.TargetSHA, TargetBranch: d.TargetBranch, InputError: d.InputError, CreatedAt: d.CreatedAt}
}

func localDeliveryFromRow(d gen.LocalDelivery) domain.LocalDelivery {
	out := domain.LocalDelivery{ID: d.ID, SessionID: domain.SessionID(d.SessionID), BaseSHA: d.BaseSha, TargetSHA: d.TargetSha, TargetBranch: d.TargetBranch, InputError: d.InputError, Decision: d.Decision, DecisionNote: d.DecisionNote, DecidedBy: domain.SessionID(d.DecidedBy), CreatedAt: d.CreatedAt}
	if d.DecidedAt.Valid {
		t := d.DecidedAt.Time
		out.DecidedAt = &t
	}
	return out
}

func (s *Store) GetLocalDelivery(ctx context.Context, id domain.SessionID) (domain.LocalDelivery, bool, error) {
	row, err := s.qr.GetLocalDelivery(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LocalDelivery{}, false, nil
	}
	if err != nil {
		return domain.LocalDelivery{}, false, err
	}
	return localDeliveryFromRow(row), true, nil
}

// ListLocalReviewFactsForSessions batches delivery, current passes and rework
// facts; the session board never performs one database query per card.
func (s *Store) ListLocalReviewFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.LocalReviewSummary, error) {
	out := map[domain.SessionID]domain.LocalReviewSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	raw := make([]string, len(ids))
	for i, id := range ids {
		raw[i] = string(id)
	}
	rows, err := s.qr.ListLocalDeliveriesForSessions(ctx, raw)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		d := localDeliveryFromRow(r)
		out[d.SessionID] = domain.LocalReviewSummary{Delivery: d}
	}
	if len(out) == 0 {
		return out, nil
	}
	runs, err := s.qr.ListLocalReviewRunsForSessions(ctx, ids)
	if err != nil {
		return nil, err
	}
	grouped := map[domain.SessionID][]domain.ReviewRun{}
	for _, r := range runs {
		grouped[r.SessionID] = append(grouped[r.SessionID], reviewRunFromRow(r))
	}
	for id, x := range out {
		if run, ok := domain.CurrentLocalReviewRun(x.Delivery.ID, grouped[id]); ok {
			x.RunID, x.Status, x.Verdict = run.ID, run.Status, run.Verdict
			out[id] = x
		}
	}
	counts, err := s.qr.LocalReworkCounts(ctx, raw)
	if err != nil {
		return nil, err
	}
	for _, r := range counts {
		x, ok := out[domain.SessionID(r.SessionID)]
		if ok {
			x.ReworkCount = r.Reworks
			out[domain.SessionID(r.SessionID)] = x
		}
	}
	return out, nil
}

func (s *Store) SetSessionAutoLocalReview(ctx context.Context, id domain.SessionID, enabled bool, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetSessionAutoLocalReview(ctx, gen.SetSessionAutoLocalReviewParams{ID: id, AutoLocalReviewEnabled: enabled, UpdatedAt: now})
	return n > 0, err
}

// QueueLocalReviewResult uses a deterministic report id. Recording the result
// and retrying delivery after a restart cannot duplicate the coordinator report.
func (s *Store) QueueLocalReviewResult(ctx context.Context, run domain.ReviewRun, note string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.EnqueueLocalReviewReport(ctx, gen.EnqueueLocalReviewReportParams{ID: "local-review-" + run.ID, SessionID: run.SessionID, Note: note, CreatedAt: now})
	return err
}

func (s *Store) DecideLocalReview(ctx context.Context, id domain.SessionID, deliveryID, decision, note string, actor domain.SessionID, now time.Time) (string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "decide local delivery", func(q *gen.Queries) error {
		rows, err := q.ListLocalReviewRunsForSessions(ctx, []domain.SessionID{id})
		if err != nil {
			return err
		}
		runs := make([]domain.ReviewRun, 0, len(rows))
		for _, r := range rows {
			runs = append(runs, reviewRunFromRow(r))
		}
		current, found := domain.CurrentLocalReviewRun(deliveryID, runs)
		if current.Status == domain.ReviewRunRunning {
			return fmt.Errorf("local review is still running")
		}
		if decision == "accepted" && (!found || current.Status != domain.ReviewRunComplete || current.Verdict != domain.VerdictApproved) {
			return fmt.Errorf("acceptance requires a completed approved review")
		}
		counts, err := q.LocalReworkCounts(ctx, []string{string(id)})
		if err != nil {
			return err
		}
		if decision == "changes_requested" && len(counts) > 0 && counts[0].Reworks >= 1 {
			decision = "needs_input"
		}
		n, err := q.DecideLocalDelivery(ctx, gen.DecideLocalDeliveryParams{ID: deliveryID, SessionID: string(id), Decision: decision, DecisionNote: note, DecidedBy: string(actor), DecidedAt: sql.NullTime{Time: now, Valid: true}})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("local delivery has changed or already has a decision")
		}
		_, err = q.EnqueueLocalReviewReport(ctx, gen.EnqueueLocalReviewReportParams{ID: "local-decision-" + deliveryID + "-" + decision, SessionID: id, Note: "Local delivery " + deliveryID + ": " + decision + ". " + note, CreatedAt: now})
		return err
	})
	return decision, err
}

func (s *Store) UndeliveredLocalReviewRuns(ctx context.Context) ([]domain.ReviewRun, error) {
	rows, err := s.qr.UndeliveredLocalReviewRuns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewRunFromRow(r))
	}
	return out, nil
}
