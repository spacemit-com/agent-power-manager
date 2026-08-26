package lifecycle

import "testing"

func TestDecideInstallState(t *testing.T) {
	install := []struct {
		manifest manifestState
		target   targetState
		want     stateDecision
	}{
		{manifestAbsent, targetAbsent, stateDecision{action: actionInstallNew}},
		{manifestAbsent, targetComplete, stateDecision{action: actionRejectConflict}},
		{manifestAbsent, targetIncomplete, stateDecision{action: actionRejectConflict}},
		{manifestAbsent, targetConflict, stateDecision{action: actionRejectConflict}},
		{manifestSameVersion, targetAbsent, stateDecision{action: actionUnchanged}},
		{manifestSameVersion, targetConflict, stateDecision{action: actionUnchanged}},
		{manifestDifferentVersion, targetComplete, stateDecision{action: actionReplace}},
		{manifestDifferentVersion, targetAbsent, stateDecision{action: actionCleanAndInstall, warnMissing: true}},
		{manifestDifferentVersion, targetIncomplete, stateDecision{action: actionCleanAndInstall, warnMissing: true}},
		{manifestDifferentVersion, targetConflict, stateDecision{action: actionRejectState}},
	}

	for _, tt := range install {
		got := decideInstallState(tt.manifest, tt.target)
		if got != tt.want {
			t.Errorf("decideInstallState(%v, %v) = %+v, want %+v", tt.manifest, tt.target, got, tt.want)
		}
	}
}

func TestDecideUninstallState(t *testing.T) {
	uninstall := []struct {
		hasManifest bool
		target      targetState
		want        stateDecision
	}{
		{false, targetComplete, stateDecision{action: actionUnexecuted}},
		{true, targetComplete, stateDecision{action: actionUninstall}},
		{true, targetAbsent, stateDecision{action: actionCleanOnly, warnMissing: true}},
		{true, targetIncomplete, stateDecision{action: actionCleanOnly, warnMissing: true}},
		{true, targetConflict, stateDecision{action: actionRejectState}},
	}

	for _, tt := range uninstall {
		got := decideUninstallState(tt.hasManifest, tt.target)
		if got != tt.want {
			t.Errorf("decideUninstallState(%v, %v) = %+v, want %+v", tt.hasManifest, tt.target, got, tt.want)
		}
	}
}
