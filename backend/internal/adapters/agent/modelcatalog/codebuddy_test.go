package modelcatalog

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCodeBuddyUsesProviderValidatedModelTextWithoutInventingACatalog(t *testing.T) {
	catalog := Base("codebuddy")
	if catalog.SelectionMode != ports.ModelSelectionText || !catalog.AllowCustom ||
		catalog.CustomModelEntry != ports.CustomModelEntryDirect || len(catalog.Models) != 0 {
		t.Fatalf("manual catalog = %+v", catalog)
	}
	if !Accepts("codebuddy", "provider-owned-model") {
		t.Fatal("the CLI must validate its own model IDs")
	}
}
