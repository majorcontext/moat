//go:generate go run generate.go

package run

import (
	_ "embed"
	"runtime"
)

// Embedded AWS credential helper binaries for Linux containers.
// These are built during development/release with:
//   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o internal/run/helpers/aws-credential-helper-linux-amd64 ./cmd/aws-credential-helper
//   GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o internal/run/helpers/aws-credential-helper-linux-arm64 ./cmd/aws-credential-helper

//go:embed helpers/aws-credential-helper-linux-amd64
var awsCredentialHelperAmd64 []byte

//go:embed helpers/aws-credential-helper-linux-arm64
var awsCredentialHelperArm64 []byte

// GetAWSCredentialHelper returns the AWS credential helper binary for the current architecture.
// This binary is designed to run in Linux containers, so it selects based on runtime.GOARCH
// which matches the container architecture when running on the same host.
func GetAWSCredentialHelper() []byte {
	if runtime.GOARCH == "arm64" {
		return awsCredentialHelperArm64
	}
	return awsCredentialHelperAmd64
}
