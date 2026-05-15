package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/phi42/ad-plugin-NetArchTest/domain"
	"github.com/phi42/ad-plugin-NetArchTest/rule"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

var rootCmd = &cobra.Command{
	Use:   "netarchtest",
	Short: "NetArchTest code generator for ADR-based DSL rules (code rules only)",
	Run: func(cmd *cobra.Command, args []string) {
		setupPluginLogger()
		if err := run(); err != nil {
			slog.Error("plugin failed", "error", err)
			os.Exit(1)
		}
	},
}

func setupPluginLogger() {
	level := slog.LevelInfo
	skipWarn := false
	switch os.Getenv("ADE_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "quiet":
		level = slog.LevelError
	case "no-warnings":
		skipWarn = true
	}
	slog.SetDefault(slog.New(newCLIHandler(os.Stderr, level, skipWarn)))
}

func Execute() {
	if len(os.Args) == 2 && os.Args[1] == "--info" {
		fmt.Println(`{"modes":["compile"]}`)
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
	// generates tests for code rules; file and custom rules are skipped.
	for _, r := range spec.Rules {
		if r.GetIsFileRule() || r.GetIsCustomRule() {
			slog.Warn(fmt.Sprintf("rule %q skipped (netarch handles code rules only)", r.GetName()))
		}
	}

	// build template data
	td, err := domain.BuildNetArchTemplateData(&spec)
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

	outDir := spec.GetOutputDir()
	if outDir == "" {
		outDir = "."
	}

	outPath := filepath.Join(outDir, fileName)

	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}

	slog.Info(fmt.Sprintf("generated %s for rules in ADR [%s]", filepath.Base(outPath), adr.Title))
	return nil
}

var nonFileToken = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeFileToken(s string) string {
	if s == "" {
		return "UNKNOWN"
	}
	return nonFileToken.ReplaceAllString(s, "_")
}
