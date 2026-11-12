// Ci runs the CI gates for the niceyaml repository. Most quality gates are
// Taskfile targets that call local tools from the devbox PATH, such as go,
// golangci-lint, and prettier. These functions run the same tasks inside the
// project's devbox environment through the devbox toolchain, so CI runs the
// same commands that developers run locally. A local run skips the container
// for speed, and CI keeps it for reproducibility.
//
// LintActions, LintCommit, and Security each compose a sibling toolchain
// directly instead, because their tools are not on the devbox PATH.
// LintActions runs the zizmor toolchain, LintCommit runs the commitlint
// toolchain, and Security runs the security toolchain, which wraps Trivy.
// LintRenovate runs a pinned renovate-config-validator in a Node container,
// because devbox and the shared toolchains do not provide the validator.
package main

import (
	"context"

	"dagger/ci/internal/dagger"
)

const (
	// commitlintConfig is the commitlint configuration file that
	// [Ci.LintCommit] uses, relative to the source root.
	commitlintConfig = ".commitlintrc.yaml"

	// renovateConfig is the Renovate configuration file that [Ci.LintRenovate]
	// validates, relative to the source root.
	renovateConfig = ".github/renovate.json5"

	// renovateImage is the Docker Official Node image. The module pulls it
	// from Docker's verified publisher space on Amazon Elastic Container
	// Registry (ECR) Public to avoid Docker Hub pull rate limits. The
	// renovateVersion constant pins the Renovate release that provides
	// renovate-config-validator.
	renovateImage   = "public.ecr.aws/docker/library/node:24-slim" // renovate: datasource=docker depName=public.ecr.aws/docker/library/node
	renovateVersion = "44.151.5"                                   // renovate: datasource=npm depName=renovate

	// zizmorConfig is the zizmor configuration file that [Ci.LintActions] uses,
	// relative to the source root.
	zizmorConfig = ".github/zizmor.yaml"

	// cacheNamespace prefixes this module's cache volumes.
	cacheNamespace = "go.jacobcolvin.com/niceyaml/ci"

	// devboxHome is the home directory of the devbox image's non-root user.
	// The env method mounts the Go and golangci-lint caches under it.
	devboxHome = "/home/devbox"
	// devboxUser is the user that owns the mounted caches, so the
	// containerized tasks can write to them.
	devboxUser = "devbox"
)

// Ci provides CI functions for the niceyaml repository. Create instances with
// [New].
type Ci struct {
	// Source is the project source directory.
	Source *dagger.Directory
	// Commitlint is the commitlint toolchain that backs [Ci.LintCommit].
	Commitlint *dagger.Commitlint // +private
	// Devbox is the devbox toolchain that the task-based checks run inside.
	Devbox *dagger.Devbox // +private
	// Scanner is the security toolchain that backs [Ci.Security] with Trivy.
	// The name Scanner avoids a collision with that method.
	Scanner *dagger.Security // +private
	// Zizmor is the zizmor toolchain that backs [Ci.LintActions].
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
		Commitlint: dag.Commitlint(dagger.CommitlintOpts{
			CacheNamespace: cacheNamespace + ":commitlint",
		}),
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
// overlaid and the Go module, Go checksum database, Go build, and
// golangci-lint caches mounted. [Ci.task] queues a Taskfile target on it. The
// caches persist across runs, so the containerized tasks reuse work the way
// the local toolchain does.
//
// Go keeps its checksum database state beside the module cache, in a
// directory the devbox user cannot create. [Ci.BuildPinned] downloads a
// module version that is missing from every go.sum file, and Go verifies
// that version against the database. The mount gives Go a directory it can
// write for that state.
func (m *Ci) env() *dagger.Container {
	owner := dagger.ContainerWithMountedCacheOpts{Owner: devboxUser}
	return m.Devbox.WithSource().
		WithMountedCache(devboxHome+"/go/pkg/mod", dag.CacheVolume(cacheNamespace+":gomod"), owner).
		WithEnvVariable("GOMODCACHE", devboxHome+"/go/pkg/mod").
		WithMountedCache(devboxHome+"/go/pkg/sumdb", dag.CacheVolume(cacheNamespace+":gosumdb"), owner).
		WithMountedCache(devboxHome+"/.cache/go-build", dag.CacheVolume(cacheNamespace+":gobuild"), owner).
		WithEnvVariable("GOCACHE", devboxHome+"/.cache/go-build").
		WithMountedCache(devboxHome+"/.cache/golangci-lint", dag.CacheVolume(cacheNamespace+":golangci-lint"), owner)
}

// task returns the devbox environment with `devbox run -- task <target>` queued
// as its next exec. Every task-based function calls it, directly or through
// [Ci.runTask], so they all run their targets the same way.
func (m *Ci) task(target string) *dagger.Container {
	return m.env().WithExec([]string{"devbox", "run", "--", "task", target})
}

// runTask runs a Taskfile target inside the devbox environment and returns an
// error when the target exits with a nonzero status.
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

// BuildPinned builds and vets the bubbles, fangs, and cmd/nyaml modules
// through `task go:build:pinned` inside the devbox environment. The target
// disables the workspace and drops the replace directive that bubbles and
// fangs hold for niceyaml. Each module then builds against the module versions
// its go.mod requires, as Go builds it outside the repository. The target also
// fails when the go.mod of cmd/nyaml has a replace or an exclude directive,
// because `go install` refuses a module that has one.
//
// +check
func (m *Ci) BuildPinned(ctx context.Context) error {
	return m.runTask(ctx, "go:build:pinned")
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
// security toolchain directly. That toolchain runs Trivy. Security scans the
// source of the `ci` toolchain, and the customization of that toolchain in
// the root dagger.json excludes the build and cache directories.
//
// +check
func (m *Ci) Security(ctx context.Context) error {
	return m.Scanner.ScanSource(ctx)
}

// SecuritySourceSarif scans source dependencies for known vulnerabilities and
// returns the results as a Static Analysis Results Interchange Format (SARIF)
// file for upload to GitHub Code Scanning. Unlike [Ci.Security], it does not
// fail on findings. The SARIF capture must produce the file even when the scan
// finds vulnerabilities, so that GitHub can show them in the Security tab. It
// scans the same source as [Ci.Security].
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

// LintCommit lints a commit message file by composing the commitlint
// toolchain directly. The commit-msg hook in .lefthook.yaml calls it with the
// message file that git names.
//
// The toolchain gets a directory that holds only .commitlintrc.yaml and an
// empty .git directory, not the checkout. commitlint takes the nearest
// directory with an entry named .git as the repository root, and it asks git
// for core.commentChar. In a linked worktree, .git is a file that points to a
// host path outside the container, so git reports a fatal error there. That
// error prints beside every rejected message and reads as its cause. With the
// empty directory, git finds no repository and reports no error, and
// commitlint strips the comment lines that start with "#". A core.commentChar
// setting in the repository has no effect on the lint.
func (m *Ci) LintCommit(
	ctx context.Context,
	// Commit message file to lint, such as .git/COMMIT_EDITMSG.
	msgFile *dagger.File,
) error {
	source := dag.Directory().
		WithFile(commitlintConfig, m.Source.File(commitlintConfig)).
		WithNewDirectory(".git")
	return m.Commitlint.Lint(ctx, source, dagger.CommitlintLintOpts{MsgFile: msgFile})
}

// LintRenovate validates the Renovate configuration with
// renovate-config-validator. It installs the validator at a pinned version in
// a Node container, so the gate is self-contained. Renovate can then update
// the version of its own validator. It is the one gate that does not compose
// devbox or a shared toolchain.
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
