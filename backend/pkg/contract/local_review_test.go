package contract_test

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestLocalDeliveryRequiresExplicitAcceptance(t *testing.T) {
	tests := []struct {
		name   string
		facts  contract.LocalKanbanFacts
		column contract.KanbanColumn
	}{
		{"submitted", contract.LocalKanbanFacts{}, contract.KanbanNeedsReview},
		{"running", contract.LocalKanbanFacts{ReviewStatus: "running"}, contract.KanbanValidating},
		{"review approval is not acceptance", contract.LocalKanbanFacts{ReviewStatus: "complete", Verdict: "approved"}, contract.KanbanNeedsReview},
		{"accepted", contract.LocalKanbanFacts{Decision: "accepted", Verdict: "approved"}, contract.KanbanReady},
		{"new commit invalidates approval", contract.LocalKanbanFacts{Decision: "accepted", Stale: true}, contract.KanbanBuilding},
		{"blocking return", contract.LocalKanbanFacts{Decision: "changes_requested"}, contract.KanbanBuilding},
		{"failed", contract.LocalKanbanFacts{ReviewStatus: "failed"}, contract.KanbanNeedsReview},
		{"user decision", contract.LocalKanbanFacts{Decision: "needs_input"}, contract.KanbanNeedsReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := contract.DeriveKanbanPresentation(contract.KanbanSessionFacts{LocalDelivery: &tt.facts}, nil, time.Now(), time.Minute)
			if p.Column != tt.column {
				t.Fatalf("placement=%+v want %s", p, tt.column)
			}
		})
	}
}
