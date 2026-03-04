// Package quickstart generates reference material for AI agent quickstart prompts.
package quickstart

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/majorcontext/moat/internal/config"
)

// GenerateSchemaReference produces a markdown reference of all moat.yaml fields
// by reflecting on the config.Config struct.
func GenerateSchemaReference() string {
	var b strings.Builder
	b.WriteString("## moat.yaml Schema Reference\n\n")
	walkStruct(reflect.TypeOf(config.Config{}), "", &b)
	return b.String()
}

// walkStruct recursively walks a struct type and writes markdown list entries
// for each field with a yaml tag.
func walkStruct(t reflect.Type, prefix string, b *strings.Builder) {
	for i := range t.NumField() {
		f := t.Field(i)

		// Skip unexported fields.
		if !f.IsExported() {
			continue
		}

		// Parse yaml tag.
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" || name == "-" {
			continue
		}

		fullName := name
		if prefix != "" {
			fullName = prefix + "." + name
		}

		ft := unwrapPtr(f.Type)

		// If the field is a struct (not from stdlib), recurse into it and
		// also emit a parent "object" line.
		if ft.Kind() == reflect.Struct && !isStdlibType(ft) {
			fmt.Fprintf(b, "- `%s` (object)\n", fullName)
			walkStruct(ft, fullName, b)
			continue
		}

		fmt.Fprintf(b, "- `%s` (%s)\n", fullName, friendlyType(ft))
	}
}

// unwrapPtr dereferences pointer types to their element type.
func unwrapPtr(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// friendlyType returns a human-readable type name for documentation.
func friendlyType(t reflect.Type) string {
	t = unwrapPtr(t)

	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice:
		elem := unwrapPtr(t.Elem())
		if elem.Kind() == reflect.String {
			return "[]string"
		}
		if elem.Kind() == reflect.Struct && !isStdlibType(elem) {
			return "[]object"
		}
		return "[]" + friendlyType(elem)
	case reflect.Map:
		key := friendlyType(t.Key())
		val := friendlyType(unwrapPtr(t.Elem()))
		return fmt.Sprintf("map[%s]%s", key, val)
	case reflect.Struct:
		return "object"
	default:
		return t.String()
	}
}

// isStdlibType returns true if the type is from the standard library or a
// built-in type (e.g., time.Time). We only recurse into types from our own
// config package.
func isStdlibType(t reflect.Type) bool {
	pkg := t.PkgPath()
	if pkg == "" {
		return true // built-in
	}
	return !strings.Contains(pkg, "majorcontext/moat")
}
