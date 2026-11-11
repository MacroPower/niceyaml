//go:build unix

package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestDiffCmdOnTerminal(t *testing.T) {
	t.Parallel()

	if runMainHelper(t) {
		return
	}

	// On a terminal, diff opens the interactive viewer on the diff of the
	// last two files.
	dir := t.TempDir()
	a := yamltest.WriteFile(t, dir, "a.yaml", "a: 1\n")
	b := yamltest.WriteFile(t, dir, "b.yaml", "a: 2\n")

	run := startOnTerminal(t, "TestDiffCmdOnTerminal", nil, "diff", a, b)

	run.readUntil(t, func(output string) bool {
		return strings.Contains(ansi.Strip(output), "diff 2/2")
	})

	_, err := run.ptmx.WriteString("q")
	require.NoError(t, err)

	_, err = run.wait(t)
	require.NoError(t, err)
}

func TestDiffCmdPrintOnTerminal(t *testing.T) {
	t.Parallel()

	if runMainHelper(t) {
		return
	}

	// With --print, diff prints the hunks to the terminal and exits rather
	// than open the interactive viewer. NO_COLOR skips the query of the
	// background, so the test does not answer one.
	dir := t.TempDir()
	a := yamltest.WriteFile(t, dir, "a.yaml", "a: 1\n")
	b := yamltest.WriteFile(t, dir, "b.yaml", "a: 2\n")

	run := startOnTerminal(t, "TestDiffCmdPrintOnTerminal", []string{"NO_COLOR=1"}, "diff", "--print", a, b)

	output, err := run.wait(t)
	require.NoError(t, err)

	got := ansi.Strip(strings.ReplaceAll(output, "\r\n", "\n"))

	assert.Contains(t, got, "--- "+a+"\n+++ "+b+"\n")
	assert.Contains(t, got, "   1 -a: 1\n")
	assert.Contains(t, got, "   1 +a: 2\n")
	assert.NotContains(t, got, "diff 2/2")
}
