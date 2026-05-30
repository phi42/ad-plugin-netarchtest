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
			out = append(out, fmt.Sprintf(`DoNotHaveNameEndingWith("%s")`, strings.ReplaceAll(e.Value, `"`, `\"`)))
		case "NameEquals":
			out = append(out, fmt.Sprintf(`DoNotHaveName("%s")`, strings.ReplaceAll(e.Value, `"`, `\"`)))
		case "NamespaceEquals":
			out = append(out, fmt.Sprintf(`DoNotResideInNamespace("%s")`, strings.ReplaceAll(e.Value, `"`, `\"`)))
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
// subject TargetRefIR, including an optional scope (in-clause) constraint.
func buildSubjectPredicate(from *rule.TargetRefIR, selMap map[string]*rule.SelectorIR) string {
	if from == nil {
		return ""
	}

	var parts []string

	if subjectFilter := buildSinglePredicate(from, selMap); subjectFilter != "" {
		parts = append(parts, subjectFilter)
	}

	if from.Scope != nil {
		if scopeNs, _ := resolveTarget(from.Scope, selMap); scopeNs != "" {
			parts = append(parts, fmt.Sprintf(`ResideInNamespace("%s")`, scopeNs))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ".\n            And().")
}

// buildSinglePredicate builds a NetArchTest predicate for a single TargetRefIR,
// without considering any scope clause.
func buildSinglePredicate(ref *rule.TargetRefIR, selMap map[string]*rule.SelectorIR) string {
	if ref == nil {
		return ""
	}

	if !ref.IsInline {
		if ns, _ := resolveTarget(ref, selMap); ns != "" {
			return fmt.Sprintf(`ResideInNamespace("%s")`, ns)
		}
		return ""
	}

	switch ref.Type {
	case rule.SelectorKind_SELECTOR_COMPONENT:
		ns := normalizeNamespace(ref.Value)
		if ns == "" {
			return ""
		}
		if ref.IsMatch {
			return fmt.Sprintf(`ResideInNamespaceMatching("%s")`, ns)
		}
		return fmt.Sprintf(`ResideInNamespace("%s")`, ns)

	case rule.SelectorKind_SELECTOR_CLASS:
		if ref.Value == "" {
			return ""
		}
		if ref.IsMatch {
			return fmt.Sprintf(`HaveNameMatching("%s")`, ref.Value)
		}
		return fmt.Sprintf(`HaveName("%s")`, ref.Value)

	case rule.SelectorKind_SELECTOR_INTERFACE:
		if ref.Value == "" {
			return ""
		}
		if ref.IsMatch {
			return fmt.Sprintf(`HaveNameMatching("%s")`, ref.Value)
		}
		return fmt.Sprintf(`HaveName("%s")`, ref.Value)

	default:
		if ref.Value != "" {
			return fmt.Sprintf(`ResideInNamespace("%s")`, normalizeNamespace(ref.Value))
		}
		return ""
	}
}
