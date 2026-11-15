package domain

import "strings"

// HarnessConversationIDPrefix marks an AO handle whose provider identity is
// scoped to the harness that created it.
const HarnessConversationIDPrefix = "ao-harness:"

// SameNativeConversationID correlates a provider's raw hook identity with AO's
// harness-scoped handle. This is not resume admission: a driver must still
// validate its persisted handle before opening the provider conversation.
func SameNativeConversationID(harness AgentHarness, left, right string) bool {
	unwrap := func(id string) (string, bool) {
		if strings.HasPrefix(id, HarnessConversationIDPrefix) {
			value, ok := strings.CutPrefix(id, HarnessConversationIDPrefix+string(harness)+":")
			return value, ok && value != "" && !strings.HasPrefix(value, HarnessConversationIDPrefix)
		}
		return id, id != ""
	}
	a, aOK := unwrap(left)
	b, bOK := unwrap(right)
	return aOK && bOK && a == b
}
