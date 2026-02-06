package util

import "os"

// CheckEnvVars returns the value of the first non-empty environment variable.
// Returns empty string if none are set.
func CheckEnvVars(names ...string) string {
	for _, name := range names {
		if val := os.Getenv(name); val != "" {
			return val
		}
	}
	return ""
}

// CheckEnvVarWithName returns the value and name of the first non-empty env var.
// Returns empty strings if none are set.
func CheckEnvVarWithName(names ...string) (value, name string) {
	for _, n := range names {
		if val := os.Getenv(n); val != "" {
			return val, n
		}
	}
	return "", ""
}
