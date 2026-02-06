// Package cli provides shared types and utilities for CLI commands.
// This package is designed to be imported by both cmd/moat/cli and
// internal/providers/*/cli.go to avoid import cycles.
//
// IMPORTANT: This package MUST NOT import internal/run or internal/providers
// to avoid import cycles. Only put types and utility functions here that
// don't depend on the run or provider packages.
package cli
