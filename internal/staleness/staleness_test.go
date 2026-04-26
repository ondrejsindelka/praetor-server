package staleness_test

import (
	"testing"

	"github.com/ondrejsindelka/praetor-server/internal/staleness"
)

func TestConstants(t *testing.T) {
	if staleness.CheckInterval >= staleness.HeartbeatTimeout {
		t.Errorf("CheckInterval (%v) must be less than HeartbeatTimeout (%v)",
			staleness.CheckInterval, staleness.HeartbeatTimeout)
	}
}
