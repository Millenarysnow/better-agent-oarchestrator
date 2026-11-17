package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

type localBranchStateStore struct {
	*branchStateStore
	facts map[domain.SessionID]domain.LocalReviewSummary
}

func (s *localBranchStateStore) ListLocalReviewFactsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID]domain.LocalReviewSummary, error) {
	return s.facts, nil
}

func TestLocalAcceptedDeliveryLeavesReadyWhenWorkspaceChanges(t *testing.T) {
	repo := newPushableRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	st := &localBranchStateStore{branchStateStore: &branchStateStore{fakeStore: newFakeStore()}, facts: map[domain.SessionID]domain.LocalReviewSummary{"ao-1": {Delivery: domain.LocalDelivery{ID: "done", TargetSHA: base, Decision: "accepted", CreatedAt: time.Now().Add(time.Minute)}}}}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "project", Harness: domain.HarnessCodex, Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: repo, DiffBaseRef: "refs/heads/main"}}
	svc := &Service{store: st, clock: time.Now}
	if err := svc.ReconcileSessionBranchState(context.Background(), "ao-1"); err != nil {
		t.Fatal(err)
	}
	sess := domain.Session{SessionRecord: st.sessions["ao-1"]}
	svc.applyLocalReview(&sess, st.facts)
	if sess.KanbanColumn != contract.KanbanReady {
		t.Fatalf("accepted delivery=%+v", sess.LocalReview)
	}
	writeWorkspaceFile(t, repo, "new.go", "uncommitted change")
	if err := svc.ReconcileSessionBranchState(context.Background(), "ao-1"); err != nil {
		t.Fatal(err)
	}
	sess.SessionRecord = st.sessions["ao-1"]
	svc.applyLocalReview(&sess, st.facts)
	if !sess.LocalReview.Stale || sess.KanbanColumn != contract.KanbanBuilding {
		t.Fatalf("dirty accepted delivery=%+v column=%s", sess.LocalReview, sess.KanbanColumn)
	}
}
