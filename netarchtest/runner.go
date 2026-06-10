package netarchtest

import (
	"bufio"
	"errors"
	"fmt"
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

// RunVerify runs `dotnet test` filtered to the generated class for adrID,
// parses per-rule outcomes, removes the generated file, and returns the results.
// The caller is responsible for writing the generated file before calling this.
//
// Required plugin_config keys:
//
//	output-dir    directory that contains the generated .g.cs file
//	test-project  path to the .csproj that owns the Generated/ folder
func RunVerify(adrID string, methodToRule map[string]string, config map[string]string) ([]VerifyResult, error) {
	outputDir := config["output-dir"]
	if outputDir == "" {
		outputDir = "."
	}
	testProject := config["test-project"]
	if testProject == "" {
		return nil, fmt.Errorf("plugin_config key \"test-project\" is required for verify mode (path to the .csproj)")
	}

	genPath := filepath.Join(outputDir, GenFileName(adrID))

	classFilter := "ArchitectureTests_ADR_" + toIdent(adrID)
	args := []string{
		"test",
		testProject,
		"--configuration", "Debug",
		"--filter", fmt.Sprintf("FullyQualifiedName~%s", classFilter),
		"--verbosity", "normal",
		"--logger", "console;verbosity=detailed",
	}

	cmd := exec.Command("dotnet", args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = os.Remove(genPath)
		return nil, fmt.Errorf("starting dotnet test: %w", err)
	}

	results := parseDotnetOutput(bufio.NewScanner(stdout), methodToRule)

	runErr := cmd.Wait()

	if removeErr := os.Remove(genPath); removeErr != nil {
		fmt.Fprintf(os.Stderr, "warn: could not remove generated file %s: %v\n", genPath, removeErr)
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
			// Normal test-failure exit — already captured in results.
			return results, nil
		}
		return results, fmt.Errorf("dotnet test failed: %w", runErr)
	}

	return results, nil
}

// parseDotnetOutput scans dotnet test console output and maps each test result
// back to its ADE rule name via methodToRule.
func parseDotnetOutput(scanner *bufio.Scanner, methodToRule map[string]string) []VerifyResult {
	var results []VerifyResult
	var currentFailed string
	var failMsgLines []string

	for scanner.Scan() {
		line := scanner.Text()
		// Forward raw dotnet output so the user sees progress.
		fmt.Fprintln(os.Stderr, line)

		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "Passed  ") || strings.HasPrefix(trimmed, "passed  ") {
			method := extractMethodName(strings.TrimPrefix(strings.TrimPrefix(trimmed, "Passed  "), "passed  "))
			ruleName := methodToRule[method]
			if ruleName == "" {
				ruleName = method
			}
			if currentFailed != "" {
				results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
				currentFailed = ""
				failMsgLines = nil
			}
			results = append(results, VerifyResult{RuleName: ruleName, Passed: true})
			continue
		}

		if strings.HasPrefix(trimmed, "Failed  ") || strings.HasPrefix(trimmed, "failed  ") {
			if currentFailed != "" {
				results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
				failMsgLines = nil
			}
			currentFailed = extractMethodName(strings.TrimPrefix(strings.TrimPrefix(trimmed, "Failed  "), "failed  "))
			continue
		}

		// Indented lines are failure message continuations.
		if currentFailed != "" && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
			failMsgLines = append(failMsgLines, strings.TrimSpace(trimmed))
		} else if currentFailed != "" && trimmed != "" {
			// Non-empty, non-indented line ends the failure block.
			results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
			currentFailed = ""
			failMsgLines = nil
		}
	}

	if currentFailed != "" {
		results = append(results, buildFailResult(currentFailed, failMsgLines, methodToRule))
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warn: reading dotnet test output: %v\n", err)
	}

	return results
}

// extractMethodName strips a trailing duration annotation like " [< 1 ms]" or " (2 ms)"
// and any namespace qualifier from a dotnet test result line.
func extractMethodName(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.LastIndex(s, " ["); idx > 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if idx := strings.LastIndex(s, " ("); idx > 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[idx+1:]
	}
	return s
}

func buildFailResult(method string, msgLines []string, methodToRule map[string]string) VerifyResult {
	ruleName := methodToRule[method]
	if ruleName == "" {
		ruleName = method
	}
	var msg string
	for _, l := range msgLines {
		if strings.HasPrefix(l, "Error Message:") || strings.HasPrefix(l, "Message:") {
			msg = strings.TrimSpace(strings.SplitN(l, ":", 2)[1])
			break
		}
	}
	if msg == "" && len(msgLines) > 0 {
		msg = msgLines[0]
	}
	return VerifyResult{RuleName: ruleName, Passed: false, Message: msg}
}
