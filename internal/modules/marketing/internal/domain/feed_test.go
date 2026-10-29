package domain_test

import (
	"regexp"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
)

func TestNewFeedKey(t *testing.T) {
	t.Parallel()
	shape := regexp.MustCompile(`^[a-z2-7]{32}$`) // the table's check
	seen := map[string]bool{}
	for range 100 {
		k, err := domain.NewFeedKey()
		if err != nil || !shape.MatchString(k) || seen[k] {
			t.Fatalf("key %q %v (seen %v)", k, err, seen[k])
		}
		seen[k] = true
	}
}

func TestSameKey(t *testing.T) {
	t.Parallel()
	const k = "abcdefghijklmnopqrstuvwxyz234567"
	for _, tc := range []struct {
		got, want string
		ok        bool
	}{{k, k, true}, {k[:31], k, false}, {k + "a", k, false}, {"", "", false}, {"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", k, false}} {
		if domain.SameKey(tc.got, tc.want) != tc.ok {
			t.Errorf("SameKey(%q, %q) != %v", tc.got, tc.want, tc.ok)
		}
	}
}

func TestFeeds(t *testing.T) {
	t.Parallel()
	for _, f := range domain.Feeds() {
		if !f.Valid() {
			t.Errorf("%s not valid", f)
		}
	}
	if domain.Feed("tiktok").Valid() {
		t.Error("tiktok valid")
	}
	if got := domain.FeedPath(domain.FeedGoogle, "k"); got != "/feeds/google.xml?key=k" {
		t.Errorf("path %s", got)
	}
}
