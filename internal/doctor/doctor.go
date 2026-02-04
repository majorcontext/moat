// Package doctor provides diagnostic output for debugging Moat.
package doctor

import "io"

// Section represents a diagnostic section that can be printed.
type Section interface {
	// Name returns the section name (e.g., "Container Runtime")
	Name() string

	// Print outputs the section's diagnostic information to the writer.
	// Returns an error if the section fails to generate diagnostics.
	Print(w io.Writer) error
}

// Registry holds all registered doctor sections.
type Registry struct {
	sections []Section
}

// NewRegistry creates a new doctor section registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a section to the registry.
func (r *Registry) Register(s Section) {
	r.sections = append(r.sections, s)
}

// Sections returns all registered sections.
func (r *Registry) Sections() []Section {
	return r.sections
}
