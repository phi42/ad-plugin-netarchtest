// domain/template.go
package domain

import (
	_ "embed"

	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/phi42/ad-plugin-NetArchTest/rule"
)

// -------------------------
// Template data structures
// -------------------------

type excludeData struct {
	Kind  string // "NameEquals" | "NameEndsWith" | "ImplementInterface"
	Value string // C#-ready value (type for interface, string for names)
}

// archTestCaseData holds data for a single NetArchTest-based test method.
type archTestCaseData struct {
	TestMethodName  string
	TypesSetup      string
	PredicatesChain string
	ConditionMethod string
	ConditionArgs   string
	AdrID           string
	AdrTitle        string
	RuleName        string
	IsWarning       bool
}

// fsCheckData holds data for a single filesystem check assertion.
type netarchTmplData struct {
	AdrID        string
	AdrTitle     string
	AdrClassName string
	HasArchTests bool
	HasSkipped   bool
	ArchTests    []archTestCaseData
	SkippedRules []string
}

// -------------------------
// Helpers
// -------------------------

var identRe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func toIdent(s string) string {
	s = identRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "Rule"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "R_" + s
	}
	return s
}

// Normalize namespaces like "X.." -> "X", "X." -> "X"
func normalizeNamespace(p string) string {
	p = strings.TrimSpace(p)
	for strings.HasSuffix(p, "..") {
		p = strings.TrimSuffix(p, "..")
	}
	for strings.HasSuffix(p, ".") {
		p = strings.TrimSuffix(p, ".")
	}
	return strings.TrimSpace(p)
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = normalizeNamespace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func quoteArgsCSV(items []string) string {
	items = uniqueNonEmpty(items)
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, s := range items {
		s = strings.ReplaceAll(s, `"`, `\"`)
		parts = append(parts, `"`+s+`"`)
	}
	return strings.Join(parts, ", ")
}

func buildTypesSetup() string {
	return `        var types = Types.InCurrentDomain();`
}

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

func buildPredicatesChain(primary string, excludes []excludeData) string {
	parts := []string{}
	if strings.TrimSpace(primary) != "" {
		parts = append(parts, primary)
	}

	exCalls := buildExcludeChain(excludes)
	parts = append(parts, exCalls...)

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

// resolveTarget resolves a TargetRefIR to a namespace pattern.
// If it's a selector reference, looks it up in selMap and returns the pattern.
// If it's an inline pattern, returns the pattern directly.
// The bool return indicates whether it was a selector reference (true) or inline (false).
func resolveTarget(target *rule.TargetRefIR, selMap map[string]*rule.SelectorIR) (string, bool) {
	if target == nil {
		return "", false
	}

	if target.IsInline {
		// Inline pattern - use directly
		return normalizeNamespace(target.Value), false
	}

	// Selector reference - lookup
	if s, ok := selMap[target.Value]; ok {
		return normalizeNamespace(s.Pattern), true
	}

	// Unknown selector reference
	return "", false
}

func inferForbiddenFromContracts(ns string) []string {
	ns = normalizeNamespace(ns)
	const needle = ".Application.Contracts"
	idx := strings.Index(ns, needle)
	if idx < 0 {
		return nil
	}
	moduleRoot := ns[:idx]
	if moduleRoot == "" {
		return nil
	}
	return []string{
		moduleRoot + ".Domain",
		moduleRoot + ".Infrastructure",
	}
}

// -------------------------
// Main builder
// -------------------------

func BuildNetArchTemplateData(spec *rule.SpecIR) (*netarchTmplData, error) {
	selMap := make(map[string]*rule.SelectorIR, len(spec.Selectors))
	for _, s := range spec.Selectors {
		selMap[s.Name] = s
	}

	var archTests []archTestCaseData
	var skippedRules []string

	for _, r := range spec.Rules {
		// Skip file rules — not supported by the netarch plugin.
		if r.GetIsFileRule() {
			continue
		}

		// Handle architecture rules
		switch r.Kind {

		case rule.RuleKind_RULE_UNSPECIFIED:
			return nil, fmt.Errorf("rule %q: no dependency constraints defined", r.Name)

		case rule.RuleKind_RULE_NOT_DEPEND:
			test, err := buildForbidTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_DEPEND_ONLY:
			test, err := buildAllowOnlyTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_ANNOTATE:
			test, err := buildAnnotateTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_NOT_ANNOTATE:
			test, err := buildNotAnnotateTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_IMPLEMENT:
			test, err := buildTypeTargetTest(spec, r, selMap, "ImplementInterface")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_NOT_IMPLEMENT:
			test, err := buildTypeTargetTest(spec, r, selMap, "NotImplementInterface")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_EXTEND:
			test, err := buildTypeTargetTest(spec, r, selMap, "Inherit")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_NOT_EXTEND:
			test, err := buildTypeTargetTest(spec, r, selMap, "NotInherit")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_IN:
			test, err := buildNamespaceCondTest(spec, r, selMap, "ResideInNamespace")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_NOT_IN:
			test, err := buildNamespaceCondTest(spec, r, selMap, "NotResideInNamespace")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_MATCH:
			test, err := buildNamePatternCondTest(spec, r, selMap, "HaveNameMatching")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_NOT_MATCH:
			test, err := buildNamePatternCondTest(spec, r, selMap, "NotHaveNameMatching")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_VISIBILITY:
			test, err := buildVisibilityTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_TYPE_CONSTRAINT:
			test, err := buildTypeConstraintTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, *test)

		case rule.RuleKind_RULE_ACCESSED_BY:
			skippedRules = append(skippedRules,
				fmt.Sprintf("%s (ACCESSED_BY: incoming dependency checks are not expressible in NetArchTest)", r.Name))

		case rule.RuleKind_RULE_ACYCLIC:
			skippedRules = append(skippedRules,
				fmt.Sprintf("%s (ACYCLIC: cycle detection is not supported by NetArchTest)", r.Name))

		default:
			return nil, fmt.Errorf("rule %q: unsupported kind %v", r.Name, r.Kind)
		}
	}

	td := &netarchTmplData{
		AdrID:        spec.Adr.Id,
		AdrTitle:     spec.Adr.Title,
		AdrClassName: "ArchitectureFrom_" + toIdent(spec.Adr.Id),
		HasArchTests: len(archTests) > 0,
		HasSkipped:   len(skippedRules) > 0,
		ArchTests:    archTests,
		SkippedRules: skippedRules,
	}
	return td, nil
}

// buildForbidTest creates a single arch test for a forbid rule.
func buildForbidTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (*archTestCaseData, error) {
	// Build the subject predicate (handles both simple and scoped subjects)
	subjectPredicate := buildSubjectPredicate(r.From, selMap)
	if subjectPredicate == "" {
		if r.From == nil {
			return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
		}
		return nil, fmt.Errorf("rule %q: cannot resolve subject for forbid rule", r.Name)
	}

	var forbidden []string
	for _, t := range r.Targets {
		ns, _ := resolveTarget(t, selMap)
		if ns != "" {
			forbidden = append(forbidden, ns)
		}
	}
	forbidden = uniqueNonEmpty(forbidden)
	sort.Strings(forbidden)

	if len(forbidden) == 0 {
		return nil, fmt.Errorf("rule %q: forbid has no resolvable targets", r.Name)
	}

	// Process exclusions
	var ex []excludeData
	for _, e := range r.Excludes {
		switch e.Kind {
		case rule.ExcludeKind_EXCLUDE_CLASS:
			ex = append(ex, excludeData{Kind: "NameEquals", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_IMPLEMENT_INTERFACE:
			ex = append(ex, excludeData{Kind: "ImplementInterface", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_COMPONENT:
			ex = append(ex, excludeData{Kind: "NamespaceEquals", Value: normalizeNamespace(e.Value)})
		}
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(subjectPredicate, ex),
		ConditionMethod: "NotHaveDependencyOnAny",
		ConditionArgs:   quoteArgsCSV(forbidden),
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildAllowOnlyTest creates a single arch test for an allow_only rule.
func buildAllowOnlyTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (*archTestCaseData, error) {
	// Build the subject predicate (handles both simple and scoped subjects)
	subjectPredicate := buildSubjectPredicate(r.From, selMap)
	if subjectPredicate == "" {
		if r.From == nil {
			return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
		}
		return nil, fmt.Errorf("rule %q: cannot resolve subject for allow_only rule", r.Name)
	}

	allowedSelNames := map[string]bool{}
	var allowedNamespaces []string
	for _, t := range r.Targets {
		ns, targetIsSel := resolveTarget(t, selMap)
		if ns == "" {
			continue
		}
		allowedNamespaces = append(allowedNamespaces, ns)
		if targetIsSel && t != nil && !t.IsInline {
			allowedSelNames[t.Value] = true
		}
	}
	allowedNamespaces = uniqueNonEmpty(allowedNamespaces)

	var forbidden []string
	for name, sel := range selMap {
		// Skip if this is the same as the 'from' selector
		if r.From != nil && !r.From.IsInline && name == r.From.Value {
			continue
		}
		if allowedSelNames[name] {
			continue
		}
		ns := normalizeNamespace(sel.Pattern)
		if ns != "" {
			forbidden = append(forbidden, ns)
		}
	}

	for _, a := range allowedNamespaces {
		forbidden = append(forbidden, inferForbiddenFromContracts(a)...)
	}

	forbidden = uniqueNonEmpty(forbidden)
	sort.Strings(forbidden)

	if len(forbidden) == 0 {
		return nil, fmt.Errorf(
			"rule %q: allow_only would be a no-op (no forbidden namespaces inferred). "+
				"Add more selectors (layers/modules) or use explicit 'forbid' rules.",
			r.Name,
		)
	}

	// Process exclusions
	var ex []excludeData
	for _, e := range r.Excludes {
		switch e.Kind {
		case rule.ExcludeKind_EXCLUDE_CLASS:
			ex = append(ex, excludeData{Kind: "NameEquals", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_IMPLEMENT_INTERFACE:
			ex = append(ex, excludeData{Kind: "ImplementInterface", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_COMPONENT:
			ex = append(ex, excludeData{Kind: "NamespaceEquals", Value: normalizeNamespace(e.Value)})
		}
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(subjectPredicate, ex),
		ConditionMethod: "NotHaveDependencyOnAny",
		ConditionArgs:   quoteArgsCSV(forbidden),
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildAnnotateTest creates a single arch test for an annotate rule.
// In C#/NetArchTest this translates to HaveCustomAttribute(typeof(AnnotationName)).
func buildAnnotateTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (*archTestCaseData, error) {
	if r.From == nil {
		return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}

	if len(r.Targets) == 0 {
		return nil, fmt.Errorf("rule %q: annotate requires an annotation name", r.Name)
	}

	annotation := r.Targets[0].Value

	// Build the predicates chain for the subject
	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve subject for annotate rule", r.Name)
	}

	// Process exclusions
	var ex []excludeData
	for _, e := range r.Excludes {
		switch e.Kind {
		case rule.ExcludeKind_EXCLUDE_CLASS:
			ex = append(ex, excludeData{Kind: "NameEquals", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_IMPLEMENT_INTERFACE:
			ex = append(ex, excludeData{Kind: "ImplementInterface", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_COMPONENT:
			ex = append(ex, excludeData{Kind: "NamespaceEquals", Value: normalizeNamespace(e.Value)})
		}
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(primaryPredicate, ex),
		ConditionMethod: "HaveCustomAttribute",
		ConditionArgs:   fmt.Sprintf("typeof(%s)", annotation),
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildNotAnnotateTest creates a single arch test for a "must not annotate" rule.
// In C#/NetArchTest this translates to NotHaveCustomAttribute(typeof(AnnotationName)).
func buildNotAnnotateTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (*archTestCaseData, error) {
	if r.From == nil {
		return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}

	if len(r.Targets) == 0 {
		return nil, fmt.Errorf("rule %q: annotate requires an annotation name", r.Name)
	}

	annotation := r.Targets[0].Value

	// Build the predicates chain for the subject
	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve subject for annotate rule", r.Name)
	}

	// Process exclusions
	var ex []excludeData
	for _, e := range r.Excludes {
		switch e.Kind {
		case rule.ExcludeKind_EXCLUDE_CLASS:
			ex = append(ex, excludeData{Kind: "NameEquals", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_IMPLEMENT_INTERFACE:
			ex = append(ex, excludeData{Kind: "ImplementInterface", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_COMPONENT:
			ex = append(ex, excludeData{Kind: "NamespaceEquals", Value: normalizeNamespace(e.Value)})
		}
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(primaryPredicate, ex),
		ConditionMethod: "NotHaveCustomAttribute",
		ConditionArgs:   fmt.Sprintf("typeof(%s)", annotation),
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// collectExcludes converts RuleIR exclusions to excludeData entries.
func collectExcludes(r *rule.RuleIR) []excludeData {
	var ex []excludeData
	for _, e := range r.Excludes {
		switch e.Kind {
		case rule.ExcludeKind_EXCLUDE_CLASS:
			ex = append(ex, excludeData{Kind: "NameEquals", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_IMPLEMENT_INTERFACE:
			ex = append(ex, excludeData{Kind: "ImplementInterface", Value: e.Value})
		case rule.ExcludeKind_EXCLUDE_COMPONENT:
			ex = append(ex, excludeData{Kind: "NamespaceEquals", Value: normalizeNamespace(e.Value)})
		}
	}
	return ex
}

// buildTypeTargetTest creates an arch test that uses typeof(X) as the condition argument.
// Used for RULE_IMPLEMENT, RULE_NOT_IMPLEMENT, RULE_EXTEND, RULE_NOT_EXTEND.
func buildTypeTargetTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR, condMethod string) (*archTestCaseData, error) {
	if r.From == nil {
		return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}
	if len(r.Targets) == 0 {
		return nil, fmt.Errorf("rule %q: %s requires a type target", r.Name, condMethod)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		ConditionMethod: condMethod,
		ConditionArgs:   fmt.Sprintf("typeof(%s)", r.Targets[0].Value),
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildNamespaceCondTest creates an arch test using namespace as a condition.
// Used for RULE_IN (ResideInNamespace) and RULE_NOT_IN (NotResideInNamespace).
func buildNamespaceCondTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR, condMethod string) (*archTestCaseData, error) {
	if r.From == nil {
		return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}
	if len(r.Targets) == 0 {
		return nil, fmt.Errorf("rule %q: %s requires a namespace target", r.Name, condMethod)
	}

	ns, _ := resolveTarget(r.Targets[0], selMap)
	if ns == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve namespace target", r.Name)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		ConditionMethod: condMethod,
		ConditionArgs:   fmt.Sprintf(`"%s"`, strings.ReplaceAll(ns, `"`, `\"`)),
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildNamePatternCondTest creates an arch test using a name regex as a condition.
// Used for RULE_MATCH (HaveNameMatching) and RULE_NOT_MATCH (NotHaveNameMatching).
func buildNamePatternCondTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR, condMethod string) (*archTestCaseData, error) {
	if r.From == nil {
		return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}
	if len(r.Targets) == 0 {
		return nil, fmt.Errorf("rule %q: %s requires a pattern target", r.Name, condMethod)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	pattern := r.Targets[0].Value

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		ConditionMethod: condMethod,
		ConditionArgs:   fmt.Sprintf(`"%s"`, strings.ReplaceAll(pattern, `"`, `\"`)),
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildVisibilityTest creates an arch test for RULE_VISIBILITY.
// Maps to BePublic(), BeInternal(), or BePrivate() in NetArchTest.
func buildVisibilityTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (*archTestCaseData, error) {
	if r.From == nil {
		return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}

	var condMethod string
	switch r.Visibility {
	case rule.Visibility_VISIBILITY_PUBLIC:
		condMethod = "BePublic"
	case rule.Visibility_VISIBILITY_INTERNAL:
		condMethod = "BeInternal"
	case rule.Visibility_VISIBILITY_PRIVATE:
		condMethod = "BePrivate"
	default:
		return nil, fmt.Errorf("rule %q: unspecified visibility", r.Name)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		ConditionMethod: condMethod,
		ConditionArgs:   "",
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildTypeConstraintTest creates an arch test for RULE_TYPE_CONSTRAINT.
// Maps to BeAbstract(), BeSealed(), or BeStatic() in NetArchTest.
func buildTypeConstraintTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (*archTestCaseData, error) {
	if r.From == nil {
		return nil, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}

	var condMethod string
	switch r.TypeConstraint {
	case rule.TypeConstraint_TYPE_CONSTRAINT_ABSTRACT:
		condMethod = "BeAbstract"
	case rule.TypeConstraint_TYPE_CONSTRAINT_SEALED:
		condMethod = "BeSealed"
	case rule.TypeConstraint_TYPE_CONSTRAINT_STATIC:
		condMethod = "BeStatic"
	default:
		return nil, fmt.Errorf("rule %q: unspecified type constraint", r.Name)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return nil, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return &archTestCaseData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      buildTypesSetup(),
		PredicatesChain: buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		ConditionMethod: condMethod,
		ConditionArgs:   "",
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}, nil
}

// buildSubjectPredicate builds the primary NetArchTest predicate for a subject TargetRefIR.
// It handles both simple subjects and scoped subjects (with "in" clause).
func buildSubjectPredicate(from *rule.TargetRefIR, selMap map[string]*rule.SelectorIR) string {
	if from == nil {
		return ""
	}

	var parts []string

	// Handle the subject's own filter (name or match)
	subjectFilter := buildSinglePredicate(from, selMap)
	if subjectFilter != "" {
		parts = append(parts, subjectFilter)
	}

	// Handle scope ("in" clause) — adds a ResideInNamespace constraint
	if from.Scope != nil {
		scopeNs, _ := resolveTarget(from.Scope, selMap)
		if scopeNs != "" {
			parts = append(parts, fmt.Sprintf(`ResideInNamespace("%s")`, scopeNs))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	// Join with .And().
	return strings.Join(parts, ".\n            And().")
}

// buildSinglePredicate builds a NetArchTest predicate for a single TargetRefIR
// (without considering scope).
func buildSinglePredicate(ref *rule.TargetRefIR, selMap map[string]*rule.SelectorIR) string {
	if ref == nil {
		return ""
	}

	if !ref.IsInline {
		// Selector reference — resolve to namespace
		ns, _ := resolveTarget(ref, selMap)
		if ns != "" {
			return fmt.Sprintf(`ResideInNamespace("%s")`, ns)
		}
		return ""
	}

	// Inline pattern
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
			// "class in ..." with no name — no class-level filter needed
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

// -------------------------
// Rendering
// -------------------------

func RenderNetArchTemplate(td *netarchTmplData) ([]byte, error) {
	funcMap := template.FuncMap{
		"verbatim": func(s string) string {
			// In C# @"..." verbatim strings, double-quotes must be doubled.
			return strings.ReplaceAll(s, `"`, `""`)
		},
	}
	tmpl, err := template.New("netarch").Funcs(funcMap).Parse(netarchTestTemplateFile)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	var b bytes.Buffer
	if err := tmpl.Execute(&b, td); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return b.Bytes(), nil
}

//go:embed netarch.tmpl
var netarchTestTemplateFile string
