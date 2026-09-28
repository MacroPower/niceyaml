# ci

This directory holds the repository's own CI module, which the root
`dagger.json` registers as `ci`. It serves only this repository. It
orchestrates the repo's `dagger -> devbox -> task` flow so CI runs the same
commands as `task check:all` runs locally.

## Functions

### Devbox Checks

- `lint`, `test`, and `test-integration` (all +check) run the matching
  Taskfile target inside the project's devbox environment through the `devbox`
  toolchain. The module mounts the Go module, Go build, and golangci-lint
  caches for these runs.
- `test-coverage` runs the coverage target the same way and returns the
  coverage profile file.
- `lint-renovate` (+check) validates the Renovate configuration with
  renovate-config-validator at a pinned version in a Node container. It is the
  one gate that runs through neither devbox nor a shared toolchain, so Renovate
  can bump its own validator.

Because the gates are Taskfile targets that call local tools, CI runs the same
commands that developers run locally. A local run skips the container for
speed, and CI keeps it for reproducibility.

### Toolchain Checks

Two gates compose a sibling toolchain directly rather than running through
devbox, because their tools are not on the devbox PATH.

- `lint-actions` (+check) lints the GitHub Actions workflows for security issues
  by composing the `zizmor` toolchain. It pins `.github/zizmor.yaml` as the
  config path rather than relying on zizmor's auto-discovery.
- `security` (+check) scans source dependencies for known vulnerabilities by
  composing the `security` toolchain (Trivy). `security-source-sarif` is the
  non-gating counterpart. It returns a SARIF file for upload to GitHub Code
  Scanning rather than failing on findings.

niceyaml ships as a library, and users `go install` the `nyaml` CLI, so this
module has no release pipeline.

## Layout

- `main.go` defines the `Ci` module (Go module path `dagger/ci`) and the check
  functions.
- `dagger.json` depends on three toolchains from `github.com/MacroPower/x`. The
  `devbox` toolchain runs the task checks, the `security` toolchain runs the
  vulnerability scan, and the `zizmor` toolchain lints the Actions workflows.
- The module has no `tests/` submodule.

The `engineVersion` in `dagger.json` must match the root `dagger.json` and the
CLI version in `.github/workflows`. Bump all three together with
`task dagger:update VERSION=<tag>`.
