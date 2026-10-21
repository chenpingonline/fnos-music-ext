package netease

import "testing"

func TestQRStateCodes(t *testing.T) {
	for _, tc := range []struct {
		code   int64
		state  string
		logged bool
	}{{800, "expired", false}, {801, "pending", false}, {802, "scanned", false}, {803, "success", true}, {404, "unknown", false}} {
		s := stateFromCode(tc.code)
		if s.State != tc.state || s.LoggedIn != tc.logged {
			t.Fatalf("%d: %+v", tc.code, s)
		}
	}
}
