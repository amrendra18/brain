# Builds, checks, and future releases

A normal build from this checkout reports `0.1.0-dev`. There is no Git tag or GitHub release for Brain. Tagged packaging is a separate future step.

The [README](../README.md#requirements) explains the pinned toolchain. `go.mod` and `go.sum` pin module versions and checksums. The first build may download them to `${XDG_CACHE_HOME:-$HOME/.cache}/brain/go-mod-cache` by default. Set `GOMODCACHE` to use another shared folder. Later builds reuse downloaded modules. `GOPROXY=off ./scripts/build` works once they are cached. The built Brain program makes no network calls.

## Portable builds and archives

Run these commands from the checkout:

~~~sh
./scripts/build all
./scripts/package --development --output .local/package-dev
~~~

The first command builds five static binaries under `.local/build`.
Targets are macOS arm64 and amd64, Windows amd64, and Linux amd64 and arm64.
Go can compile a Windows executable on macOS. This is called cross-compilation.
The script selects Windows with `GOOS=windows` and Intel/AMD 64-bit processors with `GOARCH=amd64`.
Brain uses Go-based SQLite and disables C integration, so this build needs no Windows C compiler.
The Windows output is `.local/build/windows-amd64/brain.exe`. Running Windows-specific tests still requires a Windows environment.
Each binary must be below 30,000,000 bytes.
The Intel macOS journey runs through Rosetta where Rosetta is available.
Windows and Linux builds are experimental.

Development archives say `0.1.0-dev` and must not be published.
Their binaries report `0.1.0-dev` and the actual checkout stamp.
The output folder must be new. A failed package build removes its partial output.
Without `--development`, packaging requires a clean checkout tagged `v0.1.0`.
The archives contain the binary, README, MIT LICENSE, release notes and unchanged upstream license notices.
Windows uses zip. Other targets use tar.gz. One SHA-256 file covers all five archives.
Archive timestamps are fixed so the same input bytes produce the same archive bytes.

Release notes explain the unsigned macOS and Windows warnings.
Smart App Control and enterprise policy can block an unsigned Windows binary.
Brain-authored code uses the MIT license in `LICENSE`. Third-party modules keep their upstream licenses.
For package-manager files, supply `--release-url` and `--license MIT` together.
The URL must name the chosen GitHub release's download folder.
Packaging writes `Formula/brain.rb` and `bucket/brain.json` inside its output folder.
It does not install a package manager or write to a tap or bucket repository.

The workflows are JSON documents, which are valid YAML.
Local tests check their strict schema, five runners, pinned actions and called scripts.
`check.yml` runs the full lane and an explicit FTS5 proof on each native target.
`release.yml` waits for those checks and uploads clean tagged artifacts for publication.
It has a read-only repository token.
`scripts/prepare-ci --verify` checks the Go 1.27.1 installed by setup-go on each runner. It does not copy the compiler into the checkout. Local verification uses the same command.
Windows uses Git Bash for developer scripts and native paths for Go's local caches.

Printed cmd commands use built-in Windows PowerShell with encoded arguments.
This preserves literal percent and bang characters when cmd expands variables.
PowerShell commands escape straight and curly apostrophes.
Arguments with literal double quotes use the native process argument form, including in Windows PowerShell 5.
Shell-free `next_argv` remains available. Windows recovery commands are experimental.

Workflow action pins come from [checkout 4.2.2](https://github.com/actions/checkout/releases/tag/v4.2.2),
[setup-go 5.5.0](https://github.com/actions/setup-go/releases/tag/v5.5.0), and
[upload-artifact 4.6.2](https://github.com/actions/upload-artifact/releases/tag/v4.6.2).
Shell rules follow [Microsoft's quoting documentation](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_quoting_rules).
The package-manager generators follow the [Homebrew formula guide](https://docs.brew.sh/Formula-Cookbook)
and [Scoop manifest guide](https://github.com/ScoopInstaller/Scoop/wiki/App-Manifests).

## Routine checks

```sh
./scripts/check
./scripts/check full
```

The fast lane checks formatting, host and cross-platform compilation,
unit tests, real executable journeys, the reported runtime line count and pinned module integrity.
The full lane adds race checks and coverage. Pure code repeats twenty times
when its packages exist. Build files and compiler caches stay under `.local/`. The module cache defaults to the shared folder described above.
Both scripts also run from another folder through their absolute paths.

The public retrieval yardstick uses 20 synthetic documents. Its measured 20 of 20 floor checks indexing and ranking mechanics. It does not measure search quality on a personal document collection.

The optional `scripts/evaluate` command needs your own absolute corpus root, source configuration, frozen keyword and sentence questions, and a measured floor. Pass them with `--corpus-root`, `--config`, `--questions`, `--sentences`, and `--floor`. The repository does not include private evaluation inputs.

## Stop and recover

The executable has no background process. Press Control-C to stop a command.
If Go 1.27.1 is missing, install it once and put `go` on your command path. Use `.local/go-root` for a checkout-specific installation, as the [README](../README.md#requirements) explains.
Rebuild after changing source files.

