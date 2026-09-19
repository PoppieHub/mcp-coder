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

func TestVerificationAllowsSafeScopedGoTest(t *testing.T) {
	if err := ValidateVerification([]string{"go", "test", "./internal/tools"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"go", "test", "../outside"}, {"go", "test", "./internal/../secret"}, {"go", "test", "./internal/tools;whoami"}} {
		if err := ValidateVerification(args); err == nil {
			t.Fatalf("unsafe command allowed: %q", args)
		}
	}
}
