package domain

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VerifyResult holds the outcome of a single NUnit test method mapped back to
// an ADE rule name.
type VerifyResult struct {
	RuleName string
	Passed   bool
	Message  string // failure message, empty when passed
}

// RunVerify generates a .g.cs file for spec into outputDir, runs
// `dotnet test <testProject>` filtered to the generated class, parses the
// results, removes the generated file, and returns per-rule outcomes.
//
// Required config keys (via plugin_config):
//
//	output-dir    directory where the .g.cs is written (must be inside the test project)
//	test-project  path to the .csproj that owns the Generated/ folder
//
// Optional config keys:
//
//	build-config  Debug (default) or Release
//	dotnet-args   extra arguments appended verbatim to `dotnet test`
func RunVerify(adrID string, td *netarchTmplData, config map[string]string) ([]VerifyResult, error) {
	outputDir := config["output-dir"]
	if outputDir == "" {
		outputDir = "."
	}
	testProject := config["test-project"]
	if testProject == "" {
		return nil, fmt.Errorf("plugin_config key \"test-project\" is required for verify mode (path to the .csproj)")
	}
	buildConfig := config["build-config"]
	if buildConfig == "" {
		buildConfig = "Debug"
	}
	dotnetPath := config["dotnet-path"]
	if dotnetPath == "" {
		dotnetPath = "dotnet"
	}
	extraArgs := config["dotnet-args"]

	// Generate the .g.cs file.
	csContent, err := RenderNetArchTemplate(td)
	if err != nil {
		return nil, fmt.Errorf("rendering template: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir %q: %w", outputDir, err)
	}

	sanitized := identRe.ReplaceAllString(adrID, "_")
	sanitized = strings.Trim(sanitized, "_")
	if sanitized == "" {
		sanitized = "UNKNOWN"
	}
	fileName := fmt.Sprintf("ADR_%s_netarch.g.cs", sanitized)
	genPath := filepath.Join(outputDir, fileName)

	if err := os.WriteFile(genPath, csContent, 0o644); err != nil {
		return nil, fmt.Errorf("writing generated file: %w", err)
	}
	slog.Debug("written generated file", "path", genPath)

	// Build the class name filter. The template uses "ArchitectureFrom_<id>".
	classFilter := "ArchitectureFrom_" + toIdent(adrID)

	// Run dotnet test.
	args := []string{
		"test",
		testProject,
		"--configuration", buildConfig,
		"--filter", fmt.Sprintf("FullyQualifiedName~%s", classFilter),
		"--verbosity", "normal",
		"--logger", "console;verbosity=detailed",
	}
	if extraArgs != "" {
		args = append(args, strings.Fields(extraArgs)...)
	}

	slog.Debug("running dotnet test", "args", args)
	cmd := exec.Command(dotnetPath, args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		// Clean up generated file before returning.
		_ = os.Remove(genPath)
		return nil, fmt.Errorf("starting dotnet test: %w", err)
	}

	// Parse the dotnet test console output.
	// With --verbosity normal and --logger console;verbosity=detailed, each test
	// result appears on a line like:
	//   Passed  <method> (<duration>)
	//   Failed  <method> [<duration>]
	//     <failure message lines starting with spaces>
	var results []VerifyResult
	methodToRule := buildMethodToRuleMap(td)

	scanner := bufio.NewScanner(stdout)
	var currentFailed string
	var failMsgLines []string

	for scanner.Scan() {
		line := scanner.Text()
		// Forward the raw dotnet output to stderr so the user sees progress.
		fmt.Fprintln(os.Stderr, line)

		trimmed := strings.TrimSpace(line)

		// A passing test.
		if strings.HasPrefix(trimmed, "Passed  ") || strings.HasPrefix(trimmed, "passed  ") {
			method := extractMethodName(strings.TrimPrefix(strings.TrimPrefix(trimmed, "Passed  "), "passed  "))
			rule := methodToRule[method]
			if rule == "" {
				rule = method
			}
			// Flush any pending failed entry.
			if currentFailed != "" {
				results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
				currentFailed = ""
				failMsgLines = nil
			}
			results = append(results, VerifyResult{RuleName: rule, Passed: true})
			continue
		}

		// A failing test.
		if strings.HasPrefix(trimmed, "Failed  ") || strings.HasPrefix(trimmed, "failed  ") {
			// Flush any pending failed entry.
			if currentFailed != "" {
				results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
				failMsgLines = nil
			}
			currentFailed = extractMethodName(strings.TrimPrefix(strings.TrimPrefix(trimmed, "Failed  "), "failed  "))
			continue
		}

		// Continuation lines of a failing test (indented).
		if currentFailed != "" && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
			failMsgLines = append(failMsgLines, strings.TrimSpace(trimmed))
		} else if currentFailed != "" && trimmed != "" {
			// Non-empty, non-indented line ends the failure block.
			results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
			currentFailed = ""
			failMsgLines = nil
		}
	}

	// Flush trailing failure.
	if currentFailed != "" {
		results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
	}

	// Wait for dotnet to finish. We do not return an error for test failures —
	// those are reported via the results slice. Only return an error for
	// unexpected process failures (non-test-failure exit codes like 2).
	runErr := cmd.Wait()

	// Delete the generated file now that we are done (verify is transient).
	if removeErr := os.Remove(genPath); removeErr != nil {
		slog.Warn("could not remove generated file", "path", genPath, "error", removeErr)
	}

	// If dotnet test exited with code 1 that means test failures, which we already
	// captured. Any other non-zero code is an infrastructure error.
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				// Normal test-failure exit — already captured in results.
				return results, nil
			}
		}
		return results, fmt.Errorf("dotnet test failed: %w", runErr)
	}

	return results, nil
}

// buildMethodToRuleMap builds a reverse map from NUnit test method name to ADE rule name.
func buildMethodToRuleMap(td *netarchTmplData) map[string]string {
	m := make(map[string]string, len(td.ArchTests))
	for _, t := range td.ArchTests {
		m[t.TestMethodName] = t.RuleName
	}
	return m
}

// extractMethodName strips a trailing duration annotation like " [< 1 ms]" or " (2 ms)".
func extractMethodName(s string) string {
	s = strings.TrimSpace(s)
	// Strip trailing "[...]" duration.
	if idx := strings.LastIndex(s, " ["); idx > 0 {
		s = strings.TrimSpace(s[:idx])
	}
	// Strip trailing "(...)" duration.
	if idx := strings.LastIndex(s, " ("); idx > 0 {
		s = strings.TrimSpace(s[:idx])
	}
	// Strip namespace qualifier (NUnit prints FullyQualifiedName in some modes).
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[idx+1:]
	}
	return s
}

func buildFailResult(method string, msgLines []string, methodToRule map[string]string) VerifyResult {
	rule := methodToRule[method]
	if rule == "" {
		rule = method
	}
	msg := ""
	for _, l := range msgLines {
		if strings.HasPrefix(l, "Error Message:") || strings.HasPrefix(l, "Message:") {
			msg = strings.TrimSpace(strings.SplitN(l, ":", 2)[1])
			break
		}
	}
	if msg == "" && len(msgLines) > 0 {
		msg = msgLines[0]
	}
	return VerifyResult{RuleName: rule, Passed: false, Message: msg}
}
