package domain

import "testing"

func TestSameNativeConversationID(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{"ses_1", "ses_1", true},
		{"ses_1", "ao-harness:opencode-v2:ses_1", true},
		{"ao-harness:opencode-v2:ses_1", "ses_1", true},
		{"ao-harness:opencode-v2:ses_1", "ao-harness:opencode-v2:ses_1", true},
		{"ses_1", "ao-harness:opencode:ses_1", false},
		{"ao-harness:opencode:ses_1", "ao-harness:opencode:ses_1", false},
		{"ao-harness:opencode-v2:ses_1", "ses_2", false},
		{"", "", false},
		{"ao-harness:opencode-v2:", "", false},
		{"ao-harness:opencode-v2:ao-harness:opencode:ses_1", "ses_1", false},
	} {
		if got := SameNativeConversationID(HarnessOpenCodeV2, tc.left, tc.right); got != tc.want {
			t.Errorf("SameNativeConversationID(%q, %q) = %v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
}
