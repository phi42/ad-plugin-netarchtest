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
ade config set plugin_configs.netarchtest.output-dir         ./src/Tests/ArchTests/Generated
ade config set plugin_configs.netarchtest.test-project       ./src/Tests/MyProject.Tests.csproj
ade config set plugin_configs.netarchtest.assembly-prefixes  "CompanyName.MyApp."
```

Pass `--global` to write the value to the user-level config instead of the project-level `.ade.yaml`.

| Config key                                       | Required for    | Description                                                                                                                                                                              |
| ------------------------------------------------ | --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `plugin_configs.netarchtest.output-dir`          | compile, verify | Directory in which to write generated `.g.cs` files. Defaults to `.`.                                                                                                                    |
| `plugin_configs.netarchtest.test-project`        | verify          | Path to the `.csproj` of the .NET test project.                                                                                                                                          |
| `plugin_configs.netarchtest.assembly-prefixes`   | compile         | Comma-separated assembly-name prefixes. When set, the plugin emits an `AssemblyPreloader.g.cs` alongside the test files that force-loads matching DLLs before tests run. See [Module assemblies are not loaded by default](#module-assemblies-are-not-loaded-by-default). |

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

When the rule subject uses the `class` or `interface` keyword, the generated test is prepended with `.AreClasses().And()…` (or `.AreInterfaces().And()…`) so name and namespace filters apply only to the requested kind. Plain `class` / `interface` subjects (without a name pattern) compile to `Types.InCurrentDomain().That().AreClasses()` / `…AreInterfaces()` alone.

Rules with `severity warning` will be translated to `Assert.Warn(...)` (non-fatal in NUnit). Rules with `severity error` will be translated to `Assert.That(...)`, which fails the test on violation.

## Unsupported rules

- `must only be accessed by`: NetArchTest only checks outgoing dependencies; there is no built-in reverse-dependency assertion.
- `must be acyclic`: NetArchTest has no cycle-detection condition.

Skipped rules are noted with a comment block in the generated file.

### Workarounds

Both unsupported rules can be re-expressed as one or more `must not depend on` rules, which the plugin handles natively.

**`must only be accessed by`**: invert the rule into a forbidden-incoming list, one rule per disallowed source:

```text
# Original (not supported):
#   Domain must only be accessed by Application

code "domain_access" {
  Infrastructure  must not depend on Domain
  Presentation    must not depend on Domain
  // ...one line per component that is not Application
}
```

This produces the same effect for any closed set of components. If a new component is added later, the rule must be extended manually.

**`must be acyclic`**: for a small, known set of components, encode the desired direction as pairwise `must not depend on` rules.

```text
# Original (not supported):
#   Meetings, Administration, Payments must be acyclic

code "no_cycles_between_modules" {
  Meetings        must not depend on Administration, Payments
  Administration  must not depend on Meetings, Payments
  Payments        must not depend on Meetings, Administration
}
```

For arbitrary graphs (especially ones discovered at runtime from a regex-based selector), there is no equivalent rewrite. A separate cycle-detection tool is required.

## Known limitations

### Module assemblies are not loaded by default

`Types.InCurrentDomain()` only includes assemblies that .NET has already loaded. If the test project does not explicitly reference a type from a module assembly before the test runs, that assembly is absent and rules targeting it pass vacuously.

The fix is an NUnit `[SetUpFixture]` placed in the test project's root namespace that scans the test runner's base directory and force-loads every matching assembly before any test runs.

**Auto-generated fixture.** When `plugin_configs.netarchtest.assembly-prefixes` is set, `ade compile` writes an `AssemblyPreloader.g.cs` file next to the generated tests in `output-dir`. The fixture loads any `*.dll` whose name starts with one of the configured prefixes:

```sh
ade config set plugin_configs.netarchtest.assembly-prefixes "CompanyName.MyApp."
```

For multiple prefixes use a comma-separated list (`"PrefixA.,PrefixB."`). The generated file is overwritten on every compile.

**Manual fixture.** If you prefer to maintain the fixture by hand, leave `assembly-prefixes` unset and add a file like the following to the test project:

```csharp
using System;
using System.IO;
using System.Linq;
using System.Reflection;
using NUnit.Framework;

[SetUpFixture]
public sealed class AssemblyPreloader
{
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

### Namespace patterns use prefix matching by default

The plugin defaults to NetArchTest's `ResideInNamespace` and `NotHaveDependencyOnAny`, both of which perform `StartsWith` matching. A pattern like `"MyApp.Infrastructure"` therefore also matches `"MyApp.Infrastructure.Configuration"`.

Two ways to refine the match:

- **Regex prefix.** Prefix the pattern with `regex:` in your `.rule` file or selector. The plugin switches to NetArchTest's `ResideInNamespaceMatching` / `NotResideInNamespaceMatching` / `DoNotResideInNamespaceMatching` variants and strips the `regex:` marker before emitting the C# string.

  ```text
  component "ModuleDomains" = "regex:^MyApp\.Modules\.[^.]+\.Domain$"

  code "services_live_in_module_domain" {
    class match "regex:.*Service$" must be in ModuleDomains
  }
  ```

  Regex targets are **not** supported on `must (not) depend on` rules — NetArchTest offers no regex-aware dependency method. The plugin returns a compile error in that case.
- **More specific literal prefix.** `"MyApp.Infrastructure.Persistence"` does not match `"MyApp.Infrastructure.Configuration"`.
- **Excludes.** List sub-namespaces you do not want to match as `exclude` clauses on the rule; they are emitted as `DoNotResideInNamespace(...)` (or the `*Matching` variant when the exclude value is itself regex-prefixed).

## Documentation

See [docs/implementation.md](docs/implementation.md) for a high-level explanation of the code structure and implementation design.

## License

Licensed under the [Apache License, Version 2.0](./LICENSE).
