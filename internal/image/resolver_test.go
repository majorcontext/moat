package image

import (
	"testing"

	"github.com/andybons/agentops/internal/config"
)

func TestResolveDefault(t *testing.T) {
	img := Resolve(nil)
	if img != DefaultImage {
		t.Errorf("Resolve(nil) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveNode(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.Runtime{Node: "20"},
	}
	img := Resolve(cfg)
	if img != "node:20" {
		t.Errorf("Resolve(node:20) = %q, want %q", img, "node:20")
	}
}

func TestResolvePython(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.Runtime{Python: "3.11"},
	}
	img := Resolve(cfg)
	if img != "python:3.11" {
		t.Errorf("Resolve(python:3.11) = %q, want %q", img, "python:3.11")
	}
}

func TestResolveGo(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.Runtime{Go: "1.22"},
	}
	img := Resolve(cfg)
	if img != "golang:1.22" {
		t.Errorf("Resolve(go:1.22) = %q, want %q", img, "golang:1.22")
	}
}

func TestResolvePolyglot(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.Runtime{
			Node:   "20",
			Python: "3.11",
		},
	}
	img := Resolve(cfg)
	// When both are specified, use ubuntu as base
	if img != DefaultImage {
		t.Errorf("Resolve(polyglot) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveEmptyRuntime(t *testing.T) {
	cfg := &config.Config{
		Agent: "test",
	}
	img := Resolve(cfg)
	if img != DefaultImage {
		t.Errorf("Resolve(empty runtime) = %q, want %q", img, DefaultImage)
	}
}
