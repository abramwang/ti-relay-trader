package performance

import (
	"context"
	"testing"
)

func TestT0FeeMembershipRejectsBadScopeBeforeReading(t *testing.T) {
	for _, input := range [][2]string{{"", "2026-10-12"}, {"a", "20261012"}, {"a", "bad"}} {
		if _, err := (&Service{}).ReviewT0FeeMembership(context.Background(), input[0], input[1]); err == nil {
			t.Fatal("bad scope accepted")
		}
	}
}
