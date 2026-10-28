package web

import "testing"

// TestEveryMessageTranslated: a message added to the list but not to the
// table renders as nothing, silently.
func TestEveryMessageTranslated(t *testing.T) {
	t.Parallel()
	for m := Msg(0); m < msgCount; m++ {
		if messages[m].ar == "" || messages[m].en == "" {
			t.Errorf("message %d has no text in one language: %+v", m, messages[m])
		}
	}
}
