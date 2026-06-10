package netarchtest

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

// AssemblyPreloaderFileName is the fixed name of the generated SetUpFixture file.
const AssemblyPreloaderFileName = "AssemblyPreloader.g.cs"

//go:embed preloader.tmpl
var assemblyPreloaderTemplateFile string

// ParseAssemblyPrefixes splits a comma-separated assembly-prefix list into a
// trimmed, deduplicated, ordered slice. Empty entries are dropped.
func ParseAssemblyPrefixes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// RenderAssemblyPreloader returns the C# source of a SetUpFixture that
// force-loads every assembly under the test runner's base directory whose name
// starts with one of the given prefixes.
func RenderAssemblyPreloader(prefixes []string) ([]byte, error) {
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("at least one assembly prefix is required")
	}
	tmpl, err := template.New("preloader").Parse(assemblyPreloaderTemplateFile)
	if err != nil {
		return nil, fmt.Errorf("parse preloader template: %w", err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, struct{ Prefixes []string }{Prefixes: prefixes}); err != nil {
		return nil, fmt.Errorf("execute preloader template: %w", err)
	}
	return b.Bytes(), nil
}
