package utils

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		name           string
		newVer, newSub string
		curVer, curSub string
		want           bool
	}{
		{"newer major", "2.0", "1", "1.0", "1", true},
		{"newer minor", "1.1", "1", "1.0", "1", true},
		{"newer patch", "1.0.1", "1", "1.0.0", "1", true},
		{"older", "1.0", "1", "2.0", "1", false},
		{"identical", "1.0", "1", "1.0", "1", false},

		// A rebuild of the same upstream version still needs to be upgradable:
		// same version, higher package revision.
		{"newer subversion", "1.0", "2", "1.0", "1", true},
		{"older subversion", "1.0", "1", "1.0", "2", false},

		// Version dominates subversion.
		{"newer version but lower subversion", "2.0", "1", "1.0", "9", true},
		{"older version but higher subversion", "1.0", "9", "2.0", "1", false},

		// The real case: glibc.
		{"glibc bump", "2.39", "1", "2.36", "1", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isNewer(tc.newVer, tc.newSub, tc.curVer, tc.curSub)
			if err != nil {
				t.Fatalf("isNewer: %v", err)
			}
			if got != tc.want {
				t.Errorf("isNewer(%s-%s over %s-%s) = %v, want %v",
					tc.newVer, tc.newSub, tc.curVer, tc.curSub, got, tc.want)
			}
		})
	}
}

// Unparseable versions must produce an error rather than a wrong answer: a
// silent "false" would leave a package permanently un-upgradable, and a silent
// "true" would reinstall it on every run.
func TestIsNewerRejectsUnparseableVersions(t *testing.T) {
	cases := []struct {
		name                           string
		newVer, newSub, curVer, curSub string
	}{
		{"bad repo version", "alpha", "1", "1.0", "1"},
		{"bad installed version", "1.0", "1", "beta", "1"},
		{"bad repo subversion", "1.0", "x", "1.0", "1"},
		{"bad installed subversion", "1.0", "1", "1.0", "y"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := isNewer(tc.newVer, tc.newSub, tc.curVer, tc.curSub); err == nil {
				t.Error("isNewer accepted an unparseable version, want error")
			}
		})
	}
}
