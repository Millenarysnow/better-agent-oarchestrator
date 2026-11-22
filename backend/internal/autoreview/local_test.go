package autoreview

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
)

type localTrigger struct {
	fakeTrigger
	localCalls, repairs int
}

func (f *localTrigger) TriggerLocalAuto(context.Context, domain.SessionID) (reviewcore.TriggerResult, error) {
	f.localCalls++
	return reviewcore.TriggerResult{}, nil
}
func (f *localTrigger) ReconcileLocalFeedback(context.Context) error {
	f.repairs++
	return nil
}

func TestLocalAutomaticReviewIsIndependentOfPRToggle(t *testing.T) {
	now := time.Now()
	for _, state := range []domain.ActivityState{domain.ActivityIdle, domain.ActivityActive} {
		store := &fakeStore{session: domain.SessionRecord{ID: "worker-1", Kind: domain.KindWorker, AutoLocalReviewEnabled: true, Activity: domain.Activity{State: state, LastActivityAt: now.Add(-2 * time.Minute)}}}
		trigger := &localTrigger{}
		if err := New(store, trigger, Config{Clock: func() time.Time { return now }}).Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := 0
		if state == domain.ActivityIdle {
			want = 1
		}
		if trigger.localCalls != want || trigger.calls != 0 || trigger.repairs != 1 {
			t.Fatalf("%s trigger=%+v", state, trigger)
		}
	}
}
