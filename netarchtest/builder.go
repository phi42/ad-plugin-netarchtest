package netarchtest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/phi42/ad-enforcement-tool/rule"
)

// GenFileName returns the canonical output filename for the generated C# file
// for the given ADR ID.
func GenFileName(adrID string) string {
	sanitized := identRe.ReplaceAllString(adrID, "_")
	sanitized = strings.Trim(sanitized, "_")
	if sanitized == "" {
		sanitized = "UNKNOWN"
	}
	return fmt.Sprintf("ADR_%s_NetArchTest.g.cs", sanitized)
}

// BuildNetArchTestTemplateData translates a SpecIR into template data ready for
// rendering into a C# NUnit/NetArchTest test class.
func BuildNetArchTestTemplateData(spec *rule.SpecIR) (*templateData, error) {
	selMap := make(map[string]*rule.SelectorIR, len(spec.Selectors))
	for _, s := range spec.Selectors {
		selMap[s.Name] = s
	}

	var archTests []testData
	var skippedRules []string

	for _, r := range spec.Rules {
		if r.GetIsFileRule() {
			continue
		}

		switch r.Kind {
		case rule.RuleKind_RULE_UNSPECIFIED:
			return nil, fmt.Errorf("rule %q: no dependency constraints defined", r.Name)

		case rule.RuleKind_RULE_NOT_DEPEND:
			test, err := buildForbidTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_DEPEND_ONLY:
			test, err := buildAllowOnlyTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_ANNOTATE:
			test, err := buildAnnotateTest(spec, r, selMap, "HaveCustomAttribute")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_NOT_ANNOTATE:
			test, err := buildAnnotateTest(spec, r, selMap, "NotHaveCustomAttribute")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_IMPLEMENT:
			test, err := buildTypeTargetTest(spec, r, selMap, "ImplementInterface")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_NOT_IMPLEMENT:
			test, err := buildTypeTargetTest(spec, r, selMap, "NotImplementInterface")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_EXTEND:
			test, err := buildTypeTargetTest(spec, r, selMap, "Inherit")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_NOT_EXTEND:
			test, err := buildTypeTargetTest(spec, r, selMap, "NotInherit")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_IN:
			test, err := buildNamespaceCondTest(spec, r, selMap, "ResideInNamespace")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_NOT_IN:
			test, err := buildNamespaceCondTest(spec, r, selMap, "NotResideInNamespace")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_MATCH:
			test, err := buildNamePatternCondTest(spec, r, selMap, "HaveNameMatching")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_NOT_MATCH:
			test, err := buildNamePatternCondTest(spec, r, selMap, "NotHaveNameMatching")
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_VISIBILITY:
			test, err := buildVisibilityTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

		case rule.RuleKind_RULE_TYPE_CONSTRAINT:
			test, err := buildTypeConstraintTest(spec, r, selMap)
			if err != nil {
				return nil, err
			}
			archTests = append(archTests, test)

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

	return &templateData{
		AdrID:        spec.Adr.Id,
		AdrTitle:     spec.Adr.Title,
		AdrClassName: "ArchitectureFrom_" + toIdent(spec.Adr.Id),
		HasArchTests: len(archTests) > 0,
		HasSkipped:   len(skippedRules) > 0,
		ArchTests:    archTests,
		SkippedRules: skippedRules,
	}, nil
}

// BuildMethodToRuleMap returns a map from generated NUnit test method name to
// ADE rule name, used to correlate dotnet test output back to rule names.
func BuildMethodToRuleMap(td *templateData) map[string]string {
	m := make(map[string]string, len(td.ArchTests))
	for _, t := range td.ArchTests {
		m[t.TestMethodName] = t.RuleName
	}
	return m
}

var identRe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

// toIdent converts an arbitrary string into a valid C# / Go identifier by
// replacing non-alphanumeric characters with underscores.
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

// normalizeNamespace trims surrounding whitespace and any trailing dots from p.
func normalizeNamespace(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(p, ".")
	return strings.TrimSpace(p)
}

// uniqueNonEmpty returns a deduplicated slice of non-empty, normalised strings.
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

// quoteArgsCSV deduplicates and normalises items, then returns them as a
// comma-separated list of double-quoted C# string literals.
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

// inferForbiddenFromContracts infers namespaces that a Contracts layer must not
// depend on (Domain, Infrastructure) based on the module-root convention
// "<Module>.Application.Contracts".
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

// resolveTarget resolves a TargetRefIR to a namespace pattern string.
// The bool return indicates whether it was a selector reference (true) or inline (false).
func resolveTarget(target *rule.TargetRefIR, selMap map[string]*rule.SelectorIR) (string, bool) {
	if target == nil {
		return "", false
	}
	if target.IsInline {
		return normalizeNamespace(target.Value), false
	}
	if s, ok := selMap[target.Value]; ok {
		return normalizeNamespace(s.Pattern), true
	}
	return "", false
}

// collectExcludes converts the exclude list of a rule into excludeData values.
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

// newTestCase assembles a testData value from the common fields shared by every
// generated NUnit test method.
func newTestCase(spec *rule.SpecIR, r *rule.RuleIR, predicatesChain, condMethod, condArgs string) testData {
	return testData{
		TestMethodName:  toIdent(spec.Adr.Id + "_" + r.Name),
		TypesSetup:      "        var types = Types.InCurrentDomain();",
		PredicatesChain: predicatesChain,
		ConditionMethod: condMethod,
		ConditionArgs:   condArgs,
		AdrID:           spec.Adr.Id,
		AdrTitle:        spec.Adr.Title,
		RuleName:        r.Name,
		IsWarning:       r.GetSeverity() == rule.Severity_SEVERITY_WARNING,
	}
}

// buildForbidTest creates an arch test for RULE_NOT_DEPEND.
// Maps to NotHaveDependencyOnAny with forbidden namespaces as arguments.
func buildForbidTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (testData, error) {
	subjectPredicate := buildSubjectPredicate(r.From, selMap)
	if subjectPredicate == "" {
		if r.From == nil {
			return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
		}
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject for forbid rule", r.Name)
	}

	var forbidden []string
	for _, t := range r.Targets {
		if ns, _ := resolveTarget(t, selMap); ns != "" {
			forbidden = append(forbidden, ns)
		}
	}
	forbidden = uniqueNonEmpty(forbidden)
	sort.Strings(forbidden)

	if len(forbidden) == 0 {
		return testData{}, fmt.Errorf("rule %q: forbid has no resolvable targets", r.Name)
	}

	return newTestCase(spec, r, buildPredicatesChain(subjectPredicate, collectExcludes(r)), "NotHaveDependencyOnAny", quoteArgsCSV(forbidden)), nil
}

// buildAllowOnlyTest creates an arch test for RULE_DEPEND_ONLY.
// Inverts the allowed set: all selectors not in the allowed list become forbidden.
func buildAllowOnlyTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (testData, error) {
	subjectPredicate := buildSubjectPredicate(r.From, selMap)
	if subjectPredicate == "" {
		if r.From == nil {
			return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
		}
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject for allow_only rule", r.Name)
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
		if r.From != nil && !r.From.IsInline && name == r.From.Value {
			continue
		}
		if allowedSelNames[name] {
			continue
		}
		if ns := normalizeNamespace(sel.Pattern); ns != "" {
			forbidden = append(forbidden, ns)
		}
	}
	for _, a := range allowedNamespaces {
		forbidden = append(forbidden, inferForbiddenFromContracts(a)...)
	}
	forbidden = uniqueNonEmpty(forbidden)
	sort.Strings(forbidden)

	if len(forbidden) == 0 {
		return testData{}, fmt.Errorf(
			"rule %q: allow_only would be a no-op (no forbidden namespaces inferred). "+
				"Add more selectors (layers/modules) or use explicit 'forbid' rules.",
			r.Name,
		)
	}

	return newTestCase(spec, r, buildPredicatesChain(subjectPredicate, collectExcludes(r)), "NotHaveDependencyOnAny", quoteArgsCSV(forbidden)), nil
}

// buildAnnotateTest creates an arch test for annotate/not-annotate rules.
// condMethod is "HaveCustomAttribute" or "NotHaveCustomAttribute".
func buildAnnotateTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR, condMethod string) (testData, error) {
	if r.From == nil {
		return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}
	if len(r.Targets) == 0 {
		return testData{}, fmt.Errorf("rule %q: annotate requires an annotation name", r.Name)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject for annotate rule", r.Name)
	}

	return newTestCase(spec, r,
		buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		condMethod,
		fmt.Sprintf("typeof(%s)", r.Targets[0].Value),
	), nil
}

// buildTypeTargetTest creates an arch test that uses typeof(X) as the condition argument.
// Used for RULE_IMPLEMENT, RULE_NOT_IMPLEMENT, RULE_EXTEND, RULE_NOT_EXTEND.
func buildTypeTargetTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR, condMethod string) (testData, error) {
	if r.From == nil {
		return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}
	if len(r.Targets) == 0 {
		return testData{}, fmt.Errorf("rule %q: %s requires a type target", r.Name, condMethod)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return newTestCase(spec, r,
		buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		condMethod,
		fmt.Sprintf("typeof(%s)", r.Targets[0].Value),
	), nil
}

// buildNamespaceCondTest creates an arch test using namespace as a condition.
// Used for RULE_IN (ResideInNamespace) and RULE_NOT_IN (NotResideInNamespace).
func buildNamespaceCondTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR, condMethod string) (testData, error) {
	if r.From == nil {
		return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}
	if len(r.Targets) == 0 {
		return testData{}, fmt.Errorf("rule %q: %s requires a namespace target", r.Name, condMethod)
	}

	ns, _ := resolveTarget(r.Targets[0], selMap)
	if ns == "" {
		return testData{}, fmt.Errorf("rule %q: cannot resolve namespace target", r.Name)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return newTestCase(spec, r,
		buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		condMethod,
		fmt.Sprintf(`"%s"`, strings.ReplaceAll(ns, `"`, `\"`)),
	), nil
}

// buildNamePatternCondTest creates an arch test using a name regex as a condition.
// Used for RULE_MATCH (HaveNameMatching) and RULE_NOT_MATCH (NotHaveNameMatching).
func buildNamePatternCondTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR, condMethod string) (testData, error) {
	if r.From == nil {
		return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
	}
	if len(r.Targets) == 0 {
		return testData{}, fmt.Errorf("rule %q: %s requires a pattern target", r.Name, condMethod)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return newTestCase(spec, r,
		buildPredicatesChain(primaryPredicate, collectExcludes(r)),
		condMethod,
		fmt.Sprintf(`"%s"`, strings.ReplaceAll(r.Targets[0].Value, `"`, `\"`)),
	), nil
}

// buildVisibilityTest creates an arch test for RULE_VISIBILITY.
// Maps to BePublic(), BeInternal(), or BePrivate() in NetArchTest.
func buildVisibilityTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (testData, error) {
	if r.From == nil {
		return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
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
		return testData{}, fmt.Errorf("rule %q: unspecified visibility", r.Name)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return newTestCase(spec, r, buildPredicatesChain(primaryPredicate, collectExcludes(r)), condMethod, ""), nil
}

// buildTypeConstraintTest creates an arch test for RULE_TYPE_CONSTRAINT.
// Maps to BeAbstract(), BeSealed(), or BeStatic() in NetArchTest.
func buildTypeConstraintTest(spec *rule.SpecIR, r *rule.RuleIR, selMap map[string]*rule.SelectorIR) (testData, error) {
	if r.From == nil {
		return testData{}, fmt.Errorf("rule %q: missing 'from' subject", r.Name)
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
		return testData{}, fmt.Errorf("rule %q: unspecified type constraint", r.Name)
	}

	primaryPredicate := buildSubjectPredicate(r.From, selMap)
	if primaryPredicate == "" {
		return testData{}, fmt.Errorf("rule %q: cannot resolve subject", r.Name)
	}

	return newTestCase(spec, r, buildPredicatesChain(primaryPredicate, collectExcludes(r)), condMethod, ""), nil
}
