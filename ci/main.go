// Ci runs the CI gates for the niceyaml repository. Most quality gates are
// Taskfile targets that call local tools (go, golangci-lint, prettier) from the
// devbox PATH. These functions run the same tasks inside the project's devbox
// environment through the devbox toolchain, so CI runs the same commands that
// developers run locally. A local run skips the container for speed, and CI
// keeps it for reproducibility.
//
// Two gates compose a sibling toolchain directly instead, because their tools
// are not on the devbox PATH. LintActions runs the zizmor toolchain, and
// Security runs the security toolchain (Trivy). LintRenovate runs a pinned
// renovate-config-validator in a Node container, because neither devbox nor a
// shared toolchain provides the validator.
package main

import (
	"context"

	"dagger/ci/internal/dagger"
)

const (
	// renovateConfig is the Renovate configuration file that [Ci.LintRenovate]
	// validates, relative to the source root.
	renovateConfig = ".github/renovate.json5"

	// renovateImage is the Docker Official Node image. The module pulls it
	// from Docker's verified publisher space on ECR Public to avoid Docker
	// Hub pull rate limits. renovateVersion pins the Renovate release that
	// provides renovate-config-validator.
	renovateImage   = "public.ecr.aws/docker/library/node:24-slim" // renovate: datasource=docker depName=public.ecr.aws/docker/library/node
	renovateVersion = "44.83.0"                                    // renovate: datasource=npm depName=renovate

	// zizmorConfig is the zizmor configuration file that [Ci.LintActions] uses,
	// relative to the source root.
	zizmorConfig = ".github/zizmor.yaml"

	// cacheNamespace prefixes this module's cache volumes.
	cacheNamespace = "go.jacobcolvin.com/niceyaml/ci"

	// devboxHome is the home directory of the devbox image's non-root user.
	// The env method mounts the Go and golangci-lint caches under it.
	devboxHome = "/home/devbox"
	// devboxUser owns the mounted caches so the containerized tasks can
	// write to them.
	devboxUser = "devbox"
)

// Ci provides CI functions for the niceyaml repository. Create instances with
// [New].
type Ci struct {
	// Source is the project source directory.
	Source *dagger.Directory
	// Devbox is the devbox toolchain that the task-based checks run inside.
	Devbox *dagger.Devbox // +private
	// Scanner is the security toolchain (Trivy) backing [Ci.Security]. The
	// name Scanner avoids a collision with that method.
	Scanner *dagger.Security // +private
	// Zizmor is the zizmor toolchain backing [Ci.LintActions].
	Zizmor *dagger.Zizmor // +private
}

// New creates a new [Ci] module with the given project source directory.
func New(
	// Project source directory. Ignore patterns such as .git and dist belong in
	// the root dagger.json customizations, not here.
	// +defaultPath="/"
	source *dagger.Directory,
) *Ci {
	return &Ci{
		Source: source,
		Devbox: dag.Devbox(dagger.DevboxOpts{
			Source:         source,
			CacheNamespace: cacheNamespace,
		}),
		Scanner: dag.Security(dagger.SecurityOpts{
			Source:         source,
			CacheNamespace: cacheNamespace + ":security",
		}),
		Zizmor: dag.Zizmor(dagger.ZizmorOpts{
			Source:     source,
			ConfigPath: zizmorConfig,
		}),
	}
}

// env returns the devbox environment container with the project source
// overlaid and the Go module, Go build, and golangci-lint caches mounted.
// [Ci.task] queues a Taskfile target on it. The caches persist across runs, so
// the containerized tasks reuse work the way the local toolchain does.
func (m *Ci) env() *dagger.Container {
	owner := dagger.ContainerWithMountedCacheOpts{Owner: devboxUser}
	return m.Devbox.WithSource().
		WithMountedCache(devboxHome+"/go/pkg/mod", dag.CacheVolume(cacheNamespace+":gomod"), owner).
		WithEnvVariable("GOMODCACHE", devboxHome+"/go/pkg/mod").
		WithMountedCache(devboxHome+"/.cache/go-build", dag.CacheVolume(cacheNamespace+":gobuild"), owner).
		WithEnvVariable("GOCACHE", devboxHome+"/.cache/go-build").
		WithMountedCache(devboxHome+"/.cache/golangci-lint", dag.CacheVolume(cacheNamespace+":golangci-lint"), owner)
}

// task returns the devbox environment with `devbox run -- task <target>` queued
// as its next exec. Every task-based function builds on it so they all run
// their targets the same way.
func (m *Ci) task(target string) *dagger.Container {
	return m.env().WithExec([]string{"devbox", "run", "--", "task", target})
}

// runTask runs a Taskfile target inside the devbox environment and returns an
// error if the target exits non-zero.
func (m *Ci) runTask(ctx context.Context, target string) error {
	_, err := m.task(target).Sync(ctx)
	return err
}

// Lint runs `task lint` inside the devbox environment. The target runs
// golangci-lint, the go mod tidy check, and prettier.
//
// +check
func (m *Ci) Lint(ctx context.Context) error {
	return m.runTask(ctx, "lint")
}

// Test runs the unit tests with the race detector through `task go:test`
// inside the devbox environment.
//
// +check
func (m *Ci) Test(ctx context.Context) error {
	return m.runTask(ctx, "go:test")
}

// TestIntegration runs the integration tests with the race detector through
// `task go:test:integration` inside the devbox environment.
//
// +check
func (m *Ci) TestIntegration(ctx context.Context) error {
	return m.runTask(ctx, "go:test:integration")
}

// TestCoverage runs all tests with coverage profiling through
// `task go:test:cover` inside the devbox environment and returns the coverage
// profile file.
func (m *Ci) TestCoverage() *dagger.File {
	return m.task("go:test:cover").File(".test/coverage.txt")
}

// Security scans source dependencies for known vulnerabilities by composing the
// security toolchain (Trivy) directly. It scans the `ci` toolchain's source,
// whose root dagger.json customization already excludes the build and cache
// directories.
//
// +check
func (m *Ci) Security(ctx context.Context) error {
	return m.Scanner.ScanSource(ctx)
}

// SecuritySourceSarif scans source dependencies for known vulnerabilities and
// returns the results as a SARIF file for upload to GitHub Code Scanning. Unlike
// [Ci.Security], it does not gate on findings. The SARIF capture must produce
// the file even when the scan finds vulnerabilities, so that GitHub can show
// them in the Security tab. It scans the same source as [Ci.Security].
func (m *Ci) SecuritySourceSarif() *dagger.File {
	return m.Scanner.ScanSourceSarif()
}

// LintActions lints the GitHub Actions workflows for security issues by
// composing the zizmor toolchain directly. zizmor is not on the devbox PATH, so
// this gate does not run through devbox. It pins .github/zizmor.yaml as the
// config path rather than relying on zizmor's auto-discovery.
//
// +check
func (m *Ci) LintActions(ctx context.Context) error {
	return m.Zizmor.Lint(ctx)
}

// LintRenovate validates the Renovate configuration with
// renovate-config-validator. It installs the validator at a pinned version in
// a Node container, so the gate is self-contained and Renovate can bump its own
// validator version. It is the one gate that composes neither devbox nor a
// shared toolchain.
//
// +check
func (m *Ci) LintRenovate(ctx context.Context) error {
	_, err := dag.Container().
		From(renovateImage).
		WithMountedCache("/root/.npm", dag.CacheVolume(cacheNamespace+":npm")).
		WithExec([]string{"npm", "install", "-g", "renovate@" + renovateVersion}).
		WithMountedFile("/src/"+renovateConfig, m.Source.File(renovateConfig)).
		WithWorkdir("/src").
		WithExec([]string{"renovate-config-validator", renovateConfig}).
		Sync(ctx)
	return err
}
