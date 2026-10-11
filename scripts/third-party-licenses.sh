#!/bin/sh
# Writes THIRD_PARTY_LICENSES: the license text of every module the shipped
# binary links, each under a header naming the package. go-licenses is run
# through `go run` at a pinned version so the release does not depend on what
# the runner has installed. The file is generated at release (goreleaser's
# before hook) and never committed.
set -eu
out=${1:-THIRD_PARTY_LICENSES}
version=${GO_LICENSES_VERSION:-v2.0.1}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
CGO_ENABLED=0 go run "github.com/google/go-licenses/v2@$version" save ./cmd/shhh --save_path="$tmp/save" --ignore github.com/rfizzle/shhh
: >"$out"
find "$tmp/save" -type f | sort | while read -r f; do
	printf '================================================================\n%s\n================================================================\n' "${f#"$tmp/save/"}" >>"$out"
	cat "$f" >>"$out"
	printf '\n' >>"$out"
done
