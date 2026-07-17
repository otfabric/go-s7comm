//go:build interop

package interop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapInteropMatrix(t *testing.T) {
	fixturesDir := filepath.Join("testdata", "fixtures")
	fixtures, err := loadAllFixtures(fixturesDir)
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}

	if filter := os.Getenv("SNAP_INTEROP_FIXTURE"); filter != "" {
		var filtered []*CompiledFixture
		for _, fx := range fixtures {
			if fx.ID == filter {
				filtered = append(filtered, fx)
			}
		}
		if len(filtered) == 0 {
			t.Fatalf("SNAP_INTEROP_FIXTURE=%q matched no vendored fixture", filter)
		}
		fixtures = filtered
	}

	h := newHarness(t)

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.ID, func(t *testing.T) {
			h.restartWithFixture(t, fx.Filename)
			for _, srv := range h.servers {
				srv := srv
				t.Run(srv.Name, func(t *testing.T) {
					runFixtureAgainst(t, "127.0.0.1", srv.HostPort, fx.Rack, fx.Slot, fx)
				})
			}
		})
	}
}
