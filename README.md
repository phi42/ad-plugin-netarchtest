# NetArchTest Plugin for Architectural Decision Enforcement

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](./LICENSE)

An [ad-guidance-tool](https://github.com/adr/ad-guidance-tool) enforcement plugin that compiles `code` rules from the ADE DSL into C# architecture tests using the [NetArchTest](https://github.com/BenMorris/NetArchTest) framework and NUnit. The generated `.g.cs` files are compiled as part of a .NET test project and run with `dotnet test`.

## Installation

Install from a GitHub release:

```sh
adg enforce plugin install netarchtest --repo github.com/phi42/adplugin-netarchtest
```

Or build from source and register locally:

```sh
go build -o netarchtest
adg enforce plugin install netarchtest --path ./netarchtest
```

## Prerequisites

The target .NET test project must reference the following NuGet packages:

- `NetArchTest.Rules` (>= 1.3)
- `NUnit` (>= 3.x or 4.x)
- `NUnit3TestAdapter` or `NUnit.Console` (matching NUnit version)

## Usage

```sh
adg enforce compile -i path/to/adr.rule -p netarchtest -o ./src/Tests/ArchTests
```

The plugin writes one `ADR_<id>_netarch.g.cs` file per rule file into the output directory. Run `dotnet test` in the target project to execute the generated tests.

## Supported rules

Only `code` and `file` blocks are processed. `custom` blocks are skipped with a warning.

| ADE DSL assertion                           | NetArchTest condition                         |
| ------------------------------------------- | --------------------------------------------- |
| `must not depend on`                        | `.Should().NotHaveDependencyOnAny(…)`         |
| `must only depend on`                       | `.Should().NotHaveDependencyOnAny(…)`         |
| `must implement interface`                  | `.Should().ImplementInterface(typeof(…))`     |
| `must not implement interface`              | `.Should().NotImplementInterface(typeof(…))`  |
| `must extend`                               | `.Should().Inherit(typeof(…))`                |
| `must not extend`                           | `.Should().NotInherit(typeof(…))`             |
| `must be annotated with`                    | `.Should().HaveCustomAttribute(typeof(…))`    |
| `must not be annotated with`                | `.Should().NotHaveCustomAttribute(typeof(…))` |
| `must be in` / `must not be in`             | `.Should().ResideInNamespace(…)`              |
| `must match` / `must not match`             | `.Should().HaveNameMatching(…)`               |
| `must be public/internal/private`           | `.Should().BePublic()` etc.                   |
| `must be abstract/sealed/static`            | `.Should().BeAbstract()` etc.                 |
| `file` blocks (`path … must exist/contain`) | Custom glob assertions in NUnit               |

`exclude` clauses are translated to `.And().<predicate>` entries in the `.That()` chain.

Rules with `severity warning` emit `Assert.Warn(...)` (non-fatal in NUnit). Rules with `severity error` emit `Assert.That(...)`, which fails the test on violation.

## Unsupported rules

- `must only be accessed by`: NetArchTest only checks outgoing dependencies.
- `must be acyclic`: NetArchTest has no cycle-detection condition.

Skipped rules are noted with a comment block in the generated file.

## Known limitations

`Types.InCurrentDomain()` only includes assemblies that .NET has already loaded. If the test project does not explicitly reference a type from a module assembly before the test runs, that assembly is absent, and rules targeting it pass vacuously. A common fix is an NUnit `[SetUpFixture]` that calls `Assembly.LoadFrom` on each relevant DLL before any test runs.

Namespace patterns use `StartsWith` matching in NetArchTest, so `"MyApp.Infrastructure"` also matches `"MyApp.Infrastructure.Configuration"`. Wildcard `*` characters in patterns are passed through as literals and will not match any real namespace.

## License

Licensed under the [Apache License, Version 2.0](./LICENSE).
