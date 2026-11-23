#!/usr/bin/env bash
# Merges go-yaml's scanner into this package from a git checkout of go-yaml.
#
# Usage: sync.sh /path/to/go-yaml
#
# The checkout must sit at the commit that go.mod requires, because the copy
# emits the token values of that module. Its history must hold the commit
# that UPSTREAM_COMMIT names, because the script reads the unchanged copy of
# that commit from it.
#
# The script rewrites each upstream file the same way for both commits. The
# files of the scanner package get the package clause of this package. The
# lexer gets it too, and it calls the Scanner of this package in place of
# the one that it imports. The script then merges three ways. The base is
# the unchanged copy of the commit that UPSTREAM_COMMIT names. One side is
# the copy in this package with its patches. The other side is the
# unchanged copy of the checkout. git merge-file writes the merge over the
# copy, so each patch carries over to the new commit. The script writes the
# commit of the checkout to UPSTREAM_COMMIT, and it exits with status 1 when
# a file holds conflict markers.
set -euo pipefail

up=${1:?usage: sync.sh /path/to/go-yaml}
dir=$(cd "$(dirname "$0")" && pwd)
new=$(git -C "$up" rev-parse HEAD)
old=$new
if [[ -f "$dir/UPSTREAM_COMMIT" ]]; then
	old=$(<"$dir/UPSTREAM_COMMIT")
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/base" "$tmp/new"

rewrite() {
	sed -e 's#^package scanner$#package yamlscan#' \
		-e 's#^package lexer$#package yamlscan#' \
		-e '\#^\t"github.com/goccy/go-yaml/scanner"$#d' \
		-e 's#\bscanner\.Scanner\b#Scanner#g'
}

conflicts=0
for path in scanner/scanner.go scanner/context.go scanner/error.go lexer/lexer.go; do
	name=$(basename "$path")
	git -C "$up" show "$old:$path" | rewrite >"$tmp/base/$name"
	rewrite <"$up/$path" >"$tmp/new/$name"
	if [[ ! -f "$dir/$name" ]]; then
		cp "$tmp/base/$name" "$dir/$name"
	fi
	if ! git merge-file -L copy -L "$old" -L "$new" \
		"$dir/$name" "$tmp/base/$name" "$tmp/new/$name"; then
		echo "conflict: $dir/$name" >&2
		conflicts=1
	fi
done

cp "$up/LICENSE" "$dir/LICENSE"
echo "$new" >"$dir/UPSTREAM_COMMIT"
exit "$conflicts"
