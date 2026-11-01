// relationships:
//
// references: [outdated]
package features

import "testing"

func TestReportStrictConfiguredRanges(t *testing.T) {
	for _, tag := range []string{"01", "001", "1.00", "01.0.0", "v01", "v1.00", "1.02.3", "1.2.03"} {
		if _, err := reportConstraint(tag); err == nil {
			t.Errorf("accepted %s", tag)
		}
	}
	for _, tag := range []string{"0", "0.0", "0.0.0", "1", "1.0", "v1.0", "1.0.0-beta.1", "1.x", "x"} {
		if _, err := reportConstraint(tag); err != nil {
			t.Errorf("rejected %s: %v", tag, err)
		}
	}
}
