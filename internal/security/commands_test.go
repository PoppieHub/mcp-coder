package security

import "testing"

func TestVerificationAllowlist(t *testing.T) {
	if ValidateVerification([]string{"go", "test", "./..."}) != nil {
		t.Fatal("go test rejected")
	}
	for _, a := range [][]string{{"sh", "-c", "go test"}, {"go", "test", "./...;rm"}, {"curl", "x"}} {
		if ValidateVerification(a) == nil {
			t.Fatalf("unsafe command allowed: %v", a)
		}
	}
}
