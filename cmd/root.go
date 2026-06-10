package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/phi42/ad-enforcement-tool/rule"
	"github.com/phi42/ad-plugin-NetArchTest/netarchtest"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

type pluginInfo struct {
	Modes        []string `json:"modes"`
	ConfigPrefix string   `json:"config_prefix"`
	Version      string   `json:"version,omitempty"`
}

// Version is set at build time via -ldflags.
var Version = "0.1.3-dev"

var info = pluginInfo{
	Modes:        []string{"compile", "verify"},
	ConfigPrefix: "netarchtest",
}

var rootCmd = &cobra.Command{
	Use:   "Install this plugin using `ade plugin install` and then run it via `ade compile/verify`",
	Short: "NetArchTest code generator for ADR rules (code rules only)",
	Run: func(cmd *cobra.Command, args []string) {
		if err := run(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	},
}

func Execute() {
	if len(os.Args) == 2 && os.Args[1] == "--info" {
		info.Version = Version
		out, err := json.Marshal(info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: marshaling plugin info: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
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
	// read protobuf Spec from stdin
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	var spec rule.Spec
	if err := proto.Unmarshal(payload, &spec); err != nil {
		return fmt.Errorf("unmarshal Spec protobuf: %w", err)
	}

	var skipped int
	for _, r := range spec.Rules {
		if r.GetIsFileRule() || r.GetIsCustomRule() {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "warn: %d rule(s) skipped (plugin can only handle code rules)\n", skipped)
	}

	switch spec.GetMode() {
	case rule.InvocationMode_MODE_VERIFY:
		return runVerify(&spec)
	default:
		return runCompile(&spec)
	}
}

func runCompile(spec *rule.Spec) error {
	td, err := netarchtest.BuildNetArchTestTemplateData(spec)
	if err != nil {
		return fmt.Errorf("building template data: %w", err)
	}

	if !td.HasArchTests {
		fmt.Fprintf(os.Stderr, "warn: no code rules to compile for ADR [%s], skipping file generation\n", spec.GetAdr().GetTitle())
		return nil
	}

	content, err := netarchtest.RenderNetArchTestTemplate(td)
	if err != nil {
		return fmt.Errorf("rendering template: %w", err)
	}

	filename, err := writeGeneratedFile(spec, content)
	if err != nil {
		return fmt.Errorf("writing generated test to file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "generated %s for rules in ADR [%s]\n", filename, spec.GetAdr().GetTitle())

	if preloaderName, err := writePreloaderIfConfigured(spec); err != nil {
		return fmt.Errorf("writing preloader: %w", err)
	} else if preloaderName != "" {
		fmt.Fprintf(os.Stderr, "generated %s\n", preloaderName)
	}

	return nil
}

func runVerify(spec *rule.Spec) error {
	td, err := netarchtest.BuildNetArchTestTemplateData(spec)
	if err != nil {
		return fmt.Errorf("building template data: %w", err)
	}

	if !td.HasArchTests {
		fmt.Fprintf(os.Stderr, "warn: no code rules to verify for ADR [%s], skipping\n", spec.GetAdr().GetTitle())
		return nil
	}

	content, err := netarchtest.RenderNetArchTestTemplate(td)
	if err != nil {
		return fmt.Errorf("rendering template: %w", err)
	}

	if _, err := writeGeneratedFile(spec, content); err != nil {
		return fmt.Errorf("writing generated test to file: %w", err)
	}

	results, err := netarchtest.RunVerify(spec.GetAdr().GetId(), netarchtest.BuildMethodToRuleMap(td), spec.GetPluginConfig())
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

// writeGeneratedFile creates outDir if needed and writes content to outDir/filename.
func writeGeneratedFile(spec *rule.Spec, content []byte) (string, error) {
	adr := spec.GetAdr()
	outDir := spec.GetPluginConfig()["output-dir"]
	if outDir == "" {
		outDir = "."
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("creating output directory %q: %w", outDir, err)
	}

	filename := netarchtest.GenFileName(adr.GetId())
	outPath := filepath.Join(outDir, filename)
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", outPath, err)
	}
	return filename, nil
}

// writePreloaderIfConfigured emits AssemblyPreloader.g.cs into the configured
// output-dir when plugin_configs.netarchtest.assembly-prefixes is set. Returns
// the generated filename, or "" when the config is unset.
func writePreloaderIfConfigured(spec *rule.Spec) (string, error) {
	prefixes := netarchtest.ParseAssemblyPrefixes(spec.GetPluginConfig()["assembly-prefixes"])
	if len(prefixes) == 0 {
		return "", nil
	}
	outDir := spec.GetPluginConfig()["output-dir"]
	if outDir == "" {
		outDir = "."
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("creating output directory %q: %w", outDir, err)
	}
	content, err := netarchtest.RenderAssemblyPreloader(prefixes)
	if err != nil {
		return "", err
	}
	outPath := filepath.Join(outDir, netarchtest.AssemblyPreloaderFileName)
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", outPath, err)
	}
	return netarchtest.AssemblyPreloaderFileName, nil
}
