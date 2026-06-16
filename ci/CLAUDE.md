# ci

This repository's own CI module, registered as `ci` in the root `dagger.json`.
It is not designed for remote consumption: it orchestrates the repo's
`dagger -> devbox -> task` flow so CI reproduces exactly what `task check:all`
runs locally.

## Functions

### Checks (run via devbox)

- `lint`, `test`, `test-integration` (all +check) run the matching Taskfile
  target inside the project's devbox environment via the `devbox` toolchain,
  with the Go module/build and golangci-lint caches mounted.
- `test-coverage` runs the coverage target the same way and returns the
  coverage profile file.
- `lint-renovate` (+check) validates the Renovate configuration with
  renovate-config-validator at a pinned version in a Node container — the one
  gate that runs through neither devbox nor a shared toolchain, so Renovate can
  bump its own validator.

Because the gates are Taskfile targets calling local tools, CI reproduces
exactly what developers run locally: `local` skips the container for speed, CI
keeps it for reproducibility.

### Lint actions & Security (compose sibling toolchains)

Both gates compose a sibling toolchain directly rather than running through
devbox, because their tools are not on the devbox PATH.

- `lint-actions` (+check) lints the GitHub Actions workflows for security issues
  by composing the `zizmor` toolchain. It pins `.github/zizmor.yaml` as the
  config path rather than relying on zizmor's auto-discovery.
- `security` (+check) scans source dependencies for known vulnerabilities by
  composing the `security` toolchain (Trivy). `security-source-sarif` is the
  non-gating counterpart: it returns a SARIF file rather than failing on
  findings, for upload to GitHub Code Scanning.

niceyaml is a library (the `nyaml` CLI is install-only), so this module has no
release pipeline.

## Layout

- `main.go` defines the `Ci` module (Go module path `dagger/ci`) and the check
  functions.
- Dependencies in `dagger.json`: the `devbox` toolchain (checks), the `security`
  toolchain (the vulnerability scan), and the `zizmor` toolchain (the Actions
  workflow lint), all referenced remotely from `github.com/MacroPower/x`.
- It has no `tests/` submodule.

The `engineVersion` in `dagger.json` is pinned in lockstep with the root
`dagger.json` and with the CLI version in `.github/workflows`; bump them together
via `task dagger:update VERSION=<tag>`.
