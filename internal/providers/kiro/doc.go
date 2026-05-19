// Package kiro implements the Kiro CLI agent provider for Moat.
//
// It mirrors the codex provider: a placeholder KIRO_API_KEY is set in the
// container while the Moat proxy injects the real Bearer token on the Kiro
// API hosts. Container config is staged and copied to ~/.kiro by moat-init.
package kiro
