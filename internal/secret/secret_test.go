package secret

import (
	"context"
	"os"
	"testing"
)

func TestResolve(t *testing.T) {
	symvaultGetFn = func(ctx context.Context, ref string) (string, error) {
		return "symvault-secret", nil
	}
	keychainGetFn = func(ctx context.Context, service, account string) (string, error) {
		return "keychain-secret", nil
	}

	t.Run("env", func(t *testing.T) {
		os.Setenv("TEST_ENV_VAR", "env-secret")
		defer os.Unsetenv("TEST_ENV_VAR")
		val, err := Resolve(context.Background(), "env://TEST_ENV_VAR")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "env-secret" {
			t.Errorf("expected env-secret, got %q", val)
		}
	})

	t.Run("symvault", func(t *testing.T) {
		val, err := Resolve(context.Background(), "symvault://my.ref")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "symvault-secret" {
			t.Errorf("expected symvault-secret, got %q", val)
		}
	})

	t.Run("keychain", func(t *testing.T) {
		val, err := Resolve(context.Background(), "keychain://my-service/my-account")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "keychain-secret" {
			t.Errorf("expected keychain-secret, got %q", val)
		}
	})

	t.Run("plaintext", func(t *testing.T) {
		val, err := Resolve(context.Background(), "just-a-string")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "just-a-string" {
			t.Errorf("expected just-a-string, got %q", val)
		}
	})
}
