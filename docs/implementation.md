# Implementation Overview

This document explains the code structure, design, and execution flow of the NetArchTest plugin. It covers the purpose of each file and the concepts behind the major components.

## Architecture

The plugin is a standalone Go binary. The `ade` tool invokes it by serializing a `Spec` protobuf message (which represents one parsed `.rule` file) and writing those bytes to the plugin's stdin. The plugin reads stdin, processes the rules, and communicates results through stdout (JSON info response) or stderr (progress, warnings, and errors).

The plugin supports two modes:

| Mode    | Description                                                                                                                        |
| ------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| compile | Translates `code` rules into a C# NUnit/NetArchTest test class and writes it to disk.                                              |
| verify  | Does the same translation, then runs `dotnet test` against the generated file, parses the results, and removes the temporary file. |

## Package layout

```
ad-plugin-netarchtest/
├── main.go                 entry point
├── cmd/
│    └── root.go            plugin protocol, mode dispatch, file I/O
└── netarchtest/
    ├── types.go            internal data types for the template pipeline
    ├── builder.go          rule translation (Spec → template data)
    ├── predicates.go       NetArchTest predicate string construction
    ├── render.go           Go template execution
    ├── test.tmpl           embedded C# test class template
    └── runner.go           dotnet test execution and output parsing
```

## Files

### `main.go`

The binary entry point. It delegates immediately to the `cmd` package and contains no logic of its own.

### `cmd`

#### `root.go`

Implements the plugin protocol and top-level flow:

- When invoked with `--info`, it prints a JSON descriptor listing the supported modes and config prefix, then exits. The `ade` host calls this before each invocation to verify that the plugin supports the requested mode.
- When invoked interactively (stdin is a terminal), it prints a help message and exits.
- Otherwise, it reads the serialized `Spec` protobuf from stdin and dispatches to compile or verify mode based on the mode field in the spec.

In compile mode it calls the builder, passes the result to the renderer, and writes the output file to the configured directory. In verify mode it does the same and then additionally calls into the runner to execute `dotnet test` and print per-rule pass/fail results.

### `netarchtest`

#### `types.go`

Defines the three data types that carry information through the translation pipeline:

- The top-level type holds everything needed to render one complete C# class: the ADR id and title, the class name, the list of generated test cases, and the list of rules that were skipped because NetArchTest cannot express them.
- The per-test type holds everything needed to render one NUnit test method: the method name, the predicate chain for the `.That()` call, the condition method and arguments for the `.Should()` call, and whether the rule uses warning severity.
- The exclude type is an intermediate value capturing a single exclusion filter before it is converted into a predicate string.

#### `builder.go`

The core translation layer. It iterates over the rules in the `Spec`, dispatches by rule kind, and assembles the template data structure that the renderer consumes.

Each supported rule kind maps to a specific NetArchTest condition. For dependency rules, the `from` subject (a namespace, class, or interface pattern) becomes the `.That()` predicate chain and the target namespaces become the `.Should()` condition arguments.

The most notable mapping is `must only depend on`: because NetArchTest cannot assert that something depends only on a given set, the builder inverts the allowed set into a deny-list by collecting all selectors defined in the spec that are not in the allowed list, and then uses `NotHaveDependencyOnAny` with that deny-list instead.

`exclude` clauses on any rule become additional `.And().<DoNot*>` entries appended to the `.That()` predicate chain.

Rules that cannot be expressed (incoming-dependency checks and cycle detection) are collected into a skipped list, which the template renders as comments inside the generated class.

The file also contains several utility functions for identifier sanitization, namespace normalization, deduplication, and resolving `TargetRef` values (which may be either inline literals or references to named selectors defined earlier in the rule file).

#### `predicates.go`

Contains helpers that construct the predicate chain strings that appear in the `.That()` section of each generated test. These functions work at the string level, producing code fragments that are embedded verbatim into the C# output.

The main concern here is translating the ADL concepts of subject, selector kind (component, class, interface), and scope into the corresponding NetArchTest predicate calls (`ResideInNamespace`, `ResideInNamespaceMatching`, `HaveName`, `HaveNameMatching`, `DoNotImplementInterface`, `DoNotResideInNamespace`, etc.) and joining them with `.And().`.

#### `render.go`

Executes the embedded Go template with the template data and returns the resulting C# source as bytes. The template file is embedded into the binary at build time using `//go:embed`, so the plugin has no runtime file dependencies.

#### `runner.go`

Implements the verify mode runtime. It invokes `dotnet test` scoped to the generated class, pipes the console output through a streaming line parser, and maps each `Passed`/`Failed` result back to the original ADE rule name. After the test process exits it removes the generated `.g.cs` file to avoid leaving temporary files on disk. If `dotnet test` exits with code 1 (test failures), the runner treats it as a normal outcome and returns the per-rule results; any other non-zero exit code is treated as an infrastructure error.

#### `test.tmpl`

A Go `text/template` that produces a complete, compilable C# source file containing one NUnit `[TestFixture]` class. The class has one `[Test]` method per rule. Each method:

1. Sets up `Types.InCurrentDomain()` to load all assemblies in the current .NET domain.
2. Chains `.That().<predicates>` to filter the type set to the rule subject.
3. Chains `.Should().<condition>()` to assert the constraint.
4. Calls `.GetResult()` and then either `Assert.That` (error severity) or `Assert.Warn` (warning severity) based on the outcome.

Any skipped rules are listed as comments at the bottom of the class.

## Execution flow

```
┌────────────────────┐   ┌────────────────────┐
│  ade compile       │   │  ade verify        │
│    -i adr.rule     │   │    -i adr.rule     │
│    -p netarchtest  │   │    -p netarchtest  │
└─────────┬──────────┘   └──────────┬─────────┘
          │                         │
          └────────────┬────────────┘
                       │
                       │
          (ade serializes .rule file
           writes it to plugin's stdin)
                       │
                       │
                       ▼
        ┌─────────────────────────────┐
        │         [cmd/root.go]       │
        │                             │
        │  Read Spec from stdin       │
        └──────────────┬──────────────┘
                       │
                       ▼
        ┌─────────────────────────────┐
        │   [netarchtest/builder.go]  │
        │                             │
        │  Translate code rules       │
        │  Assemble template data     │
        └──────────────┬──────────────┘
                       │
                       ▼
        ┌─────────────────────────────┐
        │  [netarchtest/render.go]    │
        │                             │
        │  Execute Go template        │
        │  => C# source bytes         │
        └──────────────┬──────────────┘
                       │
                       ▼
        ┌─────────────────────────────┐
        │         [cmd/root.go]       │
        │                             │
        │  Write test files (.g.cs)   │
        └──────────────┬──────────────┘
                       │
           ┌───────────┴───────────┐
           │                       │
        compile                 verify
           │                       │
           ▼                       ▼
   ┌───────────────┐  ┌───────────────────────────┐
   │    (done)     │  │  [netarchtest/runner.go]  │
   └───────────────┘  │                           │
                      │  Run dotnet test          │
                      │  Parse results            │
                      │  Remove .g.cs file        │
                      └───────────┬───────────────┘
                                  │
                                  ▼
                      ┌───────────────────────────┐
                      │      [cmd/root.go]        │
                      │                           │
                      │  Print pass/fail          │
                      │  per rule to stderr       │
                      └───────────────────────────┘
```
