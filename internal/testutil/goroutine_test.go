package testutil_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/testutil"
)

func TestNoLeak(t *testing.T) {
	testutil.AssertNoGoroutineLeak(t, func() {
		// nothing — should pass
	})
}
