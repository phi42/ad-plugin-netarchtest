package netarchtest

// excludeData represents a single exclusion filter applied to a NetArchTest predicate chain.
type excludeData struct {
	Kind  string // "NameEquals" | "NameEndsWith" | "ImplementInterface" | "NamespaceEquals"
	Value string // C#-ready value (type name for interface, string for name patterns)
}

// testData holds template data for a single generated NUnit test method.
type testData struct {
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

// templateData holds template data for a full ADR's generated C# test class.
type templateData struct {
	AdrID        string
	AdrTitle     string
	AdrClassName string
	HasArchTests bool
	HasSkipped   bool
	ArchTests    []testData
	SkippedRules []string
}
