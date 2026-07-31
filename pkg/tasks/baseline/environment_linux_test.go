//go:build linux
// +build linux

package tasks

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_getHostMounts(t *testing.T) {
	mounts, err := GetHostMounts()
	t.Logf("Mounts found: %v", mounts)
	assert.NoError(t, err)
}

// TestGetContainerRuntimeUIDMap pins today's (pre-fix) behaviour for the /proc/self/uid_map
// fallback, now that the read goes through the same readFile indirection as the AppArmor and
// systemd-container marker tables above it. It is a prerequisite for #6: the broadened
// "any non-identity map" rule lands in a follow-up ticket and will flip the "keeps invoking uid"
// case below from RuntimeUnknown to RuntimeBubblewrap.
func TestGetContainerRuntimeUIDMap(t *testing.T) {
	origRead, origAttr, origExists, origLandlock, origChroot := readFile, readProcAttr, fileExistsFunc, probeForLandlock, isChroot
	t.Cleanup(func() {
		readFile, readProcAttr, fileExistsFunc, probeForLandlock, isChroot = origRead, origAttr, origExists, origLandlock, origChroot
	})
	readProcAttr = func(string) string { return "" }
	fileExistsFunc = func(string) bool { return false }
	probeForLandlock = func() (bool, error) { return false, nil }
	isChroot = func() bool { return false }

	for _, tt := range []struct {
		name        string
		uidMap      string // /proc/self/uid_map contents ("" = absent/unreadable)
		wantRuntime ContainerRuntime
	}{
		// Identity map for the full range: no user namespace, unaffected by this fallback.
		{"identity map", "0 0 4294967295", RuntimeUnknown},
		// The exact shape that causes the reported defect: bwrap invoked without remapping the
		// inner user to root keeps the invoking uid. Today's inside-uid-must-be-0 check rejects
		// it, so it falls through to unknown instead of resolving to bubblewrap.
		{"map keeping invoking uid (defect)", "1001 1001 1", RuntimeUnknown},
		// Inner user remapped to root: already detected today, proving this pins current
		// behaviour rather than the yet-to-land broadening.
		{"map remapping inner user to root", "0 1001 1", RuntimeBubblewrap},
		{"absent or unreadable map", "", RuntimeUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			readFile = func(path string) ([]byte, error) {
				switch path {
				case "/proc/self/uid_map":
					if tt.uidMap == "" {
						return nil, fmt.Errorf("file not found")
					}
					return []byte(tt.uidMap), nil
				case "/run/systemd/container":
					// An unnamed marker forces identifiedRuntime to RuntimeUnknown so the final
					// no-new-privs fallback is deterministic regardless of the real process's
					// NoNewPrivs bit on the machine running the test.
					return []byte("some-manager\n"), nil
				default:
					return nil, fmt.Errorf("file not found")
				}
			}
			if got := GetContainerRuntime(0, 0); got != tt.wantRuntime {
				t.Errorf("uid_map=%q: got %v, want %v", tt.uidMap, got, tt.wantRuntime)
			}
		})
	}
}
