package netarchtest

import (
	"fmt"
	"strings"

	"github.com/phi42/ad-enforcement-tool/rule"
)

// buildExcludeChain converts a slice of excludeData into NetArchTest predicate call strings.
func buildExcludeChain(ex []excludeData) []string {
	out := make([]string, 0, len(ex))
	for _, e := range ex {
		switch e.Kind {
		case "ImplementInterface":
			out = append(out, fmt.Sprintf(`DoNotImplementInterface(typeof(%s))`, e.Value))
		case "NameEndsWith":
			out = append(out, fmt.Sprintf(`DoNotHaveNameEndingWith("%s")`, escapeQuotes(e.Value)))
		case "NameEquals":
			out = append(out, fmt.Sprintf(`DoNotHaveName("%s")`, escapeQuotes(e.Value)))
		case "NamespaceEquals":
			if e.IsRegex {
				out = append(out, fmt.Sprintf(`DoNotResideInNamespaceMatching("%s")`, escapeQuotes(e.Value)))
			} else {
				out = append(out, fmt.Sprintf(`DoNotResideInNamespace("%s")`, escapeQuotes(e.Value)))
			}
		}
	}
	return out
}

// buildPredicatesChain combines a primary predicate and a set of exclusions into
// a multi-line NetArchTest predicate chain string.
func buildPredicatesChain(primary string, excludes []excludeData) string {
	var parts []string
	if strings.TrimSpace(primary) != "" {
		parts = append(parts, primary)
	}
	parts = append(parts, buildExcludeChain(excludes)...)

	if len(parts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("            .")
	b.WriteString(parts[0])
	b.WriteString("\n")
	for _, p := range parts[1:] {
		b.WriteString("            .And().")
		b.WriteString(p)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildSubjectPredicate builds the primary NetArchTest type-filter predicate for a
// subject TargetRef, including an optional scope (in-clause) constraint.
func buildSubjectPredicate(from *rule.TargetRef, selMap map[string]*rule.Selector) string {
	if from == nil {
		return ""
	}

	var parts []string

	if subjectFilter := buildSinglePredicate(from, selMap); subjectFilter != "" {
		parts = append(parts, subjectFilter)
	}

	if from.Scope != nil {
		if scopeNs, _ := resolveTarget(from.Scope, selMap); scopeNs != "" {
			pat, isRegex := splitRegex(scopeNs)
			if isRegex {
				parts = append(parts, fmt.Sprintf(`ResideInNamespaceMatching("%s")`, escapeQuotes(pat)))
			} else {
				parts = append(parts, fmt.Sprintf(`ResideInNamespace("%s")`, escapeQuotes(pat)))
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ".\n            And().")
}

// buildSinglePredicate builds a NetArchTest predicate for a single TargetRef,
// without considering any scope clause.
func buildSinglePredicate(ref *rule.TargetRef, selMap map[string]*rule.Selector) string {
	if ref == nil {
		return ""
	}

	if !ref.IsInline {
		ns, _ := resolveTarget(ref, selMap)
		if ns == "" {
			return ""
		}
		pat, isRegex := splitRegex(ns)
		if isRegex {
			return fmt.Sprintf(`ResideInNamespaceMatching("%s")`, escapeQuotes(pat))
		}
		return fmt.Sprintf(`ResideInNamespace("%s")`, escapeQuotes(pat))
	}

	switch ref.Kind {
	case rule.SelectorKind_SELECTOR_COMPONENT:
		ns := normalizeNamespace(ref.Value)
		if ns == "" {
			return ""
		}
		pat, isRegex := splitRegex(ns)
		if ref.IsMatch || isRegex {
			return fmt.Sprintf(`ResideInNamespaceMatching("%s")`, escapeQuotes(pat))
		}
		return fmt.Sprintf(`ResideInNamespace("%s")`, escapeQuotes(pat))

	case rule.SelectorKind_SELECTOR_CLASS, rule.SelectorKind_SELECTOR_INTERFACE:
		typeFilter := "AreClasses"
		if ref.Kind == rule.SelectorKind_SELECTOR_INTERFACE {
			typeFilter = "AreInterfaces"
		}
		// Bare `class` / `interface` subject (no name pattern): emit just the
		// type filter so rules like `interface must match "regex:..."` produce a
		// resolvable subject.
		if ref.Value == "" {
			return fmt.Sprintf("%s()", typeFilter)
		}
		pat, _ := splitRegex(ref.Value)
		if ref.IsMatch {
			return fmt.Sprintf(`%s().And().HaveNameMatching("%s")`, typeFilter, escapeQuotes(pat))
		}
		return fmt.Sprintf(`%s().And().HaveName("%s")`, typeFilter, escapeQuotes(pat))

	default:
		if ref.Value == "" {
			return ""
		}
		pat, isRegex := splitRegex(normalizeNamespace(ref.Value))
		if isRegex {
			return fmt.Sprintf(`ResideInNamespaceMatching("%s")`, escapeQuotes(pat))
		}
		return fmt.Sprintf(`ResideInNamespace("%s")`, escapeQuotes(pat))
	}
}

// escapeQuotes escapes double quotes in s for embedding inside a C# string literal.
func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
