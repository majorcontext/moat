package util

import (
	"os"
	"testing"
)

func TestCheckEnvVars(t *testing.T) {
	// Clear any existing values
	os.Unsetenv("TEST_VAR_A")
	os.Unsetenv("TEST_VAR_B")

	t.Run("returns first set value", func(t *testing.T) {
		os.Setenv("TEST_VAR_B", "value_b")
		defer os.Unsetenv("TEST_VAR_B")

		got := CheckEnvVars("TEST_VAR_A", "TEST_VAR_B")
		if got != "value_b" {
			t.Errorf("expected 'value_b', got %q", got)
		}
	})

	t.Run("returns empty when none set", func(t *testing.T) {
		got := CheckEnvVars("TEST_VAR_A", "TEST_VAR_B")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("prefers first set", func(t *testing.T) {
		os.Setenv("TEST_VAR_A", "value_a")
		os.Setenv("TEST_VAR_B", "value_b")
		defer os.Unsetenv("TEST_VAR_A")
		defer os.Unsetenv("TEST_VAR_B")

		got := CheckEnvVars("TEST_VAR_A", "TEST_VAR_B")
		if got != "value_a" {
			t.Errorf("expected 'value_a', got %q", got)
		}
	})
}

func TestCheckEnvVarWithName(t *testing.T) {
	os.Unsetenv("TEST_VAR_A")
	os.Unsetenv("TEST_VAR_B")

	t.Run("returns value and name", func(t *testing.T) {
		os.Setenv("TEST_VAR_B", "value_b")
		defer os.Unsetenv("TEST_VAR_B")

		val, name := CheckEnvVarWithName("TEST_VAR_A", "TEST_VAR_B")
		if val != "value_b" {
			t.Errorf("expected value 'value_b', got %q", val)
		}
		if name != "TEST_VAR_B" {
			t.Errorf("expected name 'TEST_VAR_B', got %q", name)
		}
	})

	t.Run("returns empty when none set", func(t *testing.T) {
		val, name := CheckEnvVarWithName("TEST_VAR_A", "TEST_VAR_B")
		if val != "" || name != "" {
			t.Errorf("expected empty strings, got %q, %q", val, name)
		}
	})
}
