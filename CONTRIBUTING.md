# Contributing

Brain uses Go 1.27.1 and pinned modules from `go.mod` and `go.sum`. Install Go once and put it on your command path, or select a project installation with `.local/go-root`. The [README](README.md#requirements) gives setup commands. The first build may download modules into a shared cache. Later builds reuse them, including with `GOPROXY=off`. The built executable remains offline.

The checks and the verifier need a Git clone of this repository and `git` on your command path. A downloaded ZIP file has no `.git` folder, so they stop there. Run the quick checks from the repository root:

```sh
./scripts/check
./scripts/verify fast
```

Before proposing a code change, run `./scripts/check full` and `./scripts/verify full`. These checks use isolated test state. Build output and evidence stay under ignored `.local/`. The [build guide](docs/build-and-release.md) explains the shared module cache.

## Optional tools

1. The Git hooks in `.pre-commit-config.yaml` run [first-commit](https://github.com/amrendra18/first-commit) through [pre-commit](https://pre-commit.com). Install both tools to use the hooks. `.repo-policy.json`, `.gitleaksignore`, and `.gitmessage` hold first-commit settings. The checks above work without these tools.
2. The corpus tests in `cmd/brain/real_corpus_test.go` use synthetic files by default. To run them on your own folders, set `BRAIN_TEST_CORPUS` to a folder of projects. Also set `BRAIN_TEST_EXCLUDES_FILE` to a file that holds a JSON list of exclude patterns. Without that file, the tests skip. The tests refuse a corpus path with a folder named `brain` and skip project folders named `brain`, so they never index this checkout.

See [builds and releases](docs/build-and-release.md) for target and release limits. Report security issues through [SECURITY.md](SECURITY.md).
