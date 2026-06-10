# NetArchTest Plugin for Architectural Decision Enforcement

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](./LICENSE)

An [ad-guidance-tool](https://github.com/adr/ad-guidance-tool) enforcement plugin that compiles `code` rules into C# architecture tests using the [NetArchTest](https://github.com/BenMorris/NetArchTest) framework and NUnit. The generated `.g.cs` files are compiled as part of a .NET test project and run with `dotnet test`.

## Installation

Install from a GitHub release:

```sh
ade plugin install netarchtest --repo github.com/phi42/ad-plugin-netarchtest
```

Or build from source and register locally:

```sh
go build -o netarchtest
ade plugin install netarchtest --path ./netarchtest
```

## Prerequisites

The target .NET test project must reference the following NuGet packages:

- `NetArchTest.Rules` (>= 1.3)
- `NUnit` (>= 3.x or 4.x)
- `NUnit3TestAdapter` (matching NUnit version)
- `Microsoft.NET.Test.Sdk` (required by `dotnet test` to discover the NUnit adapter)

From the test project directory:

```sh
dotnet add package NetArchTest.Rules
dotnet add package NUnit
dotnet add package NUnit3TestAdapter
dotnet add package Microsoft.NET.Test.Sdk
```

## Usage

### Compile

```sh
ade compile -i path/to/adr.rule -p netarchtest
```

The plugin writes one `ADR_<id>_NetArchTest.g.cs` file per rule file into the output directory. Run `dotnet test` in the target project to execute the generated tests.

### Verify

```sh
ade verify -i path/to/adr.rule -p netarchtest
```

In verify mode the plugin generates the same C# file, runs `dotnet test` scoped to the generated class, maps each test result back to its ADE rule name, and removes the generated file afterward.

### Configuration

Plugin-specific options are stored under the `plugin_configs.netarchtest` namespace and forwarded to the plugin at runtime. Set them with `ade config set` from the project root:

```sh
ade config set plugin_configs.netarchtest.output-dir    ./src/Tests/ArchTests/Generated
ade config set plugin_configs.netarchtest.test-project  ./src/Tests/MyProject.Tests.csproj
```

Pass `--global` to write the value to the user-level config instead of the project-level `.ade.yaml`.

| Config key                                | Required for    | Description                                                              |
| ----------------------------------------- | --------------- | ------------------------------------------------------------------------ |
| `plugin_configs.netarchtest.output-dir`   | compile, verify | Directory in which to write the generated `.g.cs` file. Defaults to `.`. |
| `plugin_configs.netarchtest.test-project` | verify          | Path to the `.csproj` of the .NET test project.                          |

## Supported rules

Only `code` blocks are processed. `file` blocks are skipped with a warning.

| ADL assertion                     | NetArchTest condition                         |
| --------------------------------- | --------------------------------------------- |
| `must not depend on`              | `.Should().NotHaveDependencyOnAny(…)`         |
| `must only depend on`             | `.Should().NotHaveDependencyOnAny(…)`         |
| `must implement interface`        | `.Should().ImplementInterface(typeof(…))`     |
| `must not implement interface`    | `.Should().NotImplementInterface(typeof(…))`  |
| `must extend`                     | `.Should().Inherit(typeof(…))`                |
| `must not extend`                 | `.Should().NotInherit(typeof(…))`             |
| `must be annotated with`          | `.Should().HaveCustomAttribute(typeof(…))`    |
| `must not be annotated with`      | `.Should().NotHaveCustomAttribute(typeof(…))` |
| `must be in`                      | `.Should().ResideInNamespace(…)`              |
| `must not be in`                  | `.Should().NotResideInNamespace(…)`           |
| `must match`                      | `.Should().HaveNameMatching(…)`               |
| `must not match`                  | `.Should().NotHaveNameMatching(…)`            |
| `must be public/internal/private` | `.Should().BePublic()` etc.                   |
| `must be abstract/sealed/static`  | `.Should().BeAbstract()` etc.                 |

`exclude` clauses are translated to `.And().<predicate>` entries in the `.That()` chain.

Rules with `severity warning` will be translated to `Assert.Warn(...)` (non-fatal in NUnit). Rules with `severity error` will be translated to `Assert.That(...)`, which fails the test on violation.

## Unsupported rules

- `must only be accessed by`: NetArchTest only checks outgoing dependencies.
- `must be acyclic`: NetArchTest has no cycle-detection condition.

Skipped rules are noted with a comment block in the generated file.

## Known limitations

### Module assemblies are not loaded by default

`Types.InCurrentDomain()` only includes assemblies that .NET has already loaded. If the test project does not explicitly reference a type from a module assembly before the test runs, that assembly is absent and rules targeting it pass vacuously.

The fix is an NUnit `[SetUpFixture]` placed in the test project's root namespace that scans the test runner's base directory and force-loads every relevant assembly before any test runs:

```csharp
using System;
using System.IO;
using System.Linq;
using System.Reflection;
using NUnit.Framework;

[SetUpFixture]
public sealed class AssemblyPreloader
{
    // TODO: change to the assembly-name prefix(es) you want to enforce rules on.
    private static readonly string[] Prefixes = { "MyApp." };

    [OneTimeSetUp]
    public void LoadModuleAssemblies()
    {
        var loaded = AppDomain.CurrentDomain.GetAssemblies()
            .Select(a => a.GetName().Name)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);

        foreach (var dll in Directory.EnumerateFiles(AppContext.BaseDirectory, "*.dll"))
        {
            var name = Path.GetFileNameWithoutExtension(dll);
            if (!Prefixes.Any(p => name.StartsWith(p, StringComparison.OrdinalIgnoreCase))) continue;
            if (loaded.Contains(name)) continue;
            Assembly.LoadFrom(dll);
        }
    }
}
```

A `[SetUpFixture]` declared without a namespace runs once before every test in the assembly. Without it, rules targeting modules other than the one hosting the tests will silently pass.

### Namespace patterns use prefix matching

The plugin uses NetArchTest's `ResideInNamespace` and `NotHaveDependencyOnAny`, both of which perform `StartsWith` matching. Two consequences:

- A pattern like `"MyApp.Infrastructure"` also matches `"MyApp.Infrastructure.Configuration"`.
- Wildcards (`*`, `**`) and the `regex:` prefix from the DSL are passed through as literal strings; they will not match any real namespace.

The available workarounds are:

- Use a more specific prefix. `"MyApp.Infrastructure.Persistence"` does not match `"MyApp.Infrastructure.Configuration"`.
- Exclude the sub-namespaces you do not want to match by listing them as additional `component` selectors and adding `exclude` clauses to the rule.

For anything beyond these workarounds (e.g. matching `Modules.*.Domain` across an unknown set of modules), there is no current way to express it with this plugin.

## Documentation

See [docs/implementation.md](docs/implementation.md) for a high-level explanation of the code structure and implementation design.

## License

Licensed under the [Apache License, Version 2.0](./LICENSE).
