package netarchtest

import (
	_ "embed"

	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// RenderNetArchTestTemplate executes the embedded C# test template with the
// provided template data and returns the rendered file contents.
func RenderNetArchTestTemplate(td *templateData) ([]byte, error) {
	funcMap := template.FuncMap{
		"verbatim": func(s string) string {
			// In C# @"..." verbatim strings, double-quotes must be doubled.
			return strings.ReplaceAll(s, `"`, `""`)
		},
	}
	tmpl, err := template.New("netarchtest").Funcs(funcMap).Parse(netarchTestTemplateFile)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	var b bytes.Buffer
	if err := tmpl.Execute(&b, td); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return b.Bytes(), nil
}

//go:embed test.tmpl
var netarchTestTemplateFile string
