#!/usr/bin/env bash
# Copies go-yaml's decoder into this package from a checkout of go-yaml.
#
# Usage: sync.sh /path/to/go-yaml
#
# The checkout must sit at the commit that go.mod requires, because the copy
# builds on that module for the ast, token, and parser packages. The script
# rewrites the package clause and the import paths of go-yaml's internal
# packages, and it changes nothing else. It also ports go-yaml's decoder and
# fuzz tests. The ported decoder tests use go-yaml's own Marshal and
# FormatError, which belong to the encoder that niceyaml does not copy. Two
# failure messages of the port print an int with %v in place of %q, because
# the printf check of go vet refuses %q for an int, and go test runs that
# check.
set -euo pipefail

up=${1:?usage: sync.sh /path/to/go-yaml}
dir=$(cd "$(dirname "$0")" && pwd)
pkg=go.jacobcolvin.com/niceyaml/internal/yamldecode

rewrite() {
	sed -e 's#^package yaml$#package yamldecode#' \
		-e "s#\"github.com/goccy/go-yaml/internal/errors\"#\"$pkg/errors\"#" \
		-e "s#\"github.com/goccy/go-yaml/internal/format\"#\"$pkg/format\"#" \
		"$1"
}

mkdir -p "$dir/format" "$dir/testdata"
rewrite "$up/decode.go" >"$dir/decode.go"
rewrite "$up/context.go" >"$dir/context.go"
rewrite "$up/struct.go" >"$dir/struct.go"
cp "$up/internal/format/format.go" "$dir/format/format.go"
cp "$up/LICENSE" "$dir/LICENSE"
git -C "$up" rev-parse HEAD >"$dir/UPSTREAM_COMMIT"

sed -e 's#^package yaml_test$#package yamldecode_test#' \
	-e "s#^\t\"github.com/goccy/go-yaml\"\$#\tup \"github.com/goccy/go-yaml\"\n\tyaml \"$pkg\"#" \
	-e "s#\"github.com/goccy/go-yaml/internal/errors\"#\"$pkg/errors\"#" \
	-e 's#yaml\.Marshal(#up.Marshal(#g' \
	-e 's#yaml\.FormatError(#up.FormatError(#g' \
	-e '/^func TestUnmarshalablePtrInt(/,/^}/s#but %q is set#but %v is set#' \
	"$up/decode_test.go" >"$dir/decode_test.go"
cp "$up/testdata/anchor.yml" "$dir/testdata/anchor.yml"

sed -e 's#^package yaml_test$#package yamldecode_test#' \
	-e "s#^\t\"github.com/goccy/go-yaml\"\$#\tyaml \"$pkg\"#" \
	"$up/fuzz_test.go" >"$dir/yaml_fuzz_test.go"
