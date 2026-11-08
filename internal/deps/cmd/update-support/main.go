// update-support refreshes the embedded upstream support catalog and registry.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/deps/support"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := update(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func update(root string) error {
	now := time.Now().UTC()
	fetcher := support.Fetcher{}
	catalog, err := fetcher.Fetch(context.Background(), now)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "internal/deps/registry.yaml")
	registry, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := support.UpdateRegistry(registry, catalog, now)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// Prepare and validate both outputs before changing either tracked file.
	if err := os.WriteFile(filepath.Join(root, "internal/deps/support/catalog.json"), data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return err
	}
	for _, name := range []string{"node", "python", "go", "postgres", "mysql", "redis", "ollama"} {
		fmt.Printf("%s: %v\n", name, catalog.Dependencies[name].Versions(now))
	}
	return nil
}
