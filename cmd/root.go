package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/phi42/ad-enforcement-tool/rule"
	"github.com/phi42/ad-plugin-NetArchTest/domain"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

var rootCmd = &cobra.Command{
	Use:   "netarchtest",
	Short: "NetArchTest code generator for ADR-based DSL rules (code rules only)",
	Run: func(cmd *cobra.Command, args []string) {
		if err := run(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	},
}

func Execute() {
	if len(os.Args) == 2 && os.Args[1] == "--info" {
		fmt.Println(`{"modes":["compile","verify"],"config_prefix":"netarchtest"}`)
		os.Exit(0)
	}
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		_ = rootCmd.Help()
		os.Exit(0)
	}
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func run() error {
	// read protobuf SpecIR from stdin
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	var spec rule.SpecIR
	if err := proto.Unmarshal(payload, &spec); err != nil {
		return fmt.Errorf("unmarshal SpecIR protobuf: %w", err)
	}

	// Warn about any rules this plugin does not handle. netarch only
	// handles code rules; file and custom rules are skipped.
	for _, r := range spec.Rules {
		if r.GetIsFileRule() || r.GetIsCustomRule() {
			fmt.Fprintf(os.Stderr, "warn: rule %q skipped (netarch handles code rules only)\n", r.GetName())
		}
	}

	switch spec.GetMode() {
	case rule.InvocationMode_MODE_VERIFY:
		return runVerify(&spec)
	default:
		return runCompile(&spec)
	}
}

func runCompile(spec *rule.SpecIR) error {
	// build template data
	td, err := domain.BuildNetArchTemplateData(spec)
	if err != nil {
		return fmt.Errorf("building template data: %w", err)
	}

	// render C# file
	out, err := domain.RenderNetArchTemplate(td)
	if err != nil {
		return fmt.Errorf("rendering template: %w", err)
	}

	// write output
	adr := spec.GetAdr()
	adrID := "UNKNOWN"
	if adr != nil && adr.GetId() != "" {
		adrID = adr.GetId()
	}

	fileName := fmt.Sprintf("ADR_%s_netarch.g.cs", sanitizeFileToken(adrID))

	outDir := spec.GetPluginConfig()["output-dir"]
	if outDir == "" {
		outDir = "."
	}

	outPath := filepath.Join(outDir, fileName)

	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}

	fmt.Fprintf(os.Stderr, "generated %s for rules in ADR [%s]\n", filepath.Base(outPath), adr.Title)
	return nil
}

func runVerify(spec *rule.SpecIR) error {
	adr := spec.GetAdr()
	adrID := "UNKNOWN"
	if adr != nil && adr.GetId() != "" {
		adrID = adr.GetId()
	}

	td, err := domain.BuildNetArchTemplateData(spec)
	if err != nil {
		return fmt.Errorf("building template data: %w", err)
	}

	results, err := domain.RunVerify(adrID, td, spec.GetPluginConfig())
	if err != nil {
		return err
	}

	hasFailures := false
	for _, res := range results {
		if res.Passed {
			fmt.Fprintf(os.Stderr, "passed [%s]\n", res.RuleName)
		} else {
			if res.Message != "" {
				fmt.Fprintf(os.Stderr, "error: failed [%s]: %s\n", res.RuleName, res.Message)
			} else {
				fmt.Fprintf(os.Stderr, "error: failed [%s]\n", res.RuleName)
			}
			hasFailures = true
		}
	}

	if hasFailures {
		return fmt.Errorf("one or more architecture rules failed")
	}
	return nil
}

var nonFileToken = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeFileToken(s string) string {
	if s == "" {
		return "UNKNOWN"
	}
	return nonFileToken.ReplaceAllString(s, "_")
}
