package id_test

import (
	"fmt"

	"github.com/majorcontext/moat/internal/id"
)

func ExampleGenerate() {
	// Generate always produces an id that IsValid accepts for the same prefix.
	runID := id.Generate("run")
	fmt.Println(id.IsValid(runID, "run"))
	// Output: true
}

func ExampleIsValid() {
	fmt.Println(id.IsValid("run_a1b2c3d4e5f6", "run")) // well-formed
	fmt.Println(id.IsValid("run_a1b2c3d4e5f6", "vol")) // wrong prefix
	fmt.Println(id.IsValid("run_xyz", "run"))          // suffix not 12 hex chars
	// Output:
	// true
	// false
	// false
}
