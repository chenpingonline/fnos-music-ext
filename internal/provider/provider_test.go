package provider

import "testing"

func TestUserRequiresVerifiedRealGUID(t *testing.T) {
	for _, u := range []User{{}, {FnOSGUID: "shared", Verified: true}, {FnOSGUID: "a", Verified: false}} {
		if u.Validate() != ErrUnauthenticated {
			t.Fatalf("should refuse %+v", u)
		}
	}
	if err := (User{FnOSGUID: "abc", Verified: true}).Validate(); err != nil {
		t.Fatal(err)
	}
}
