#!/usr/bin/env bash
#
# build-analyser.sh <package> — build a Go analyser's newest release against
# the newest golang.org/x/tools, into $(go env GOPATH)/bin.
#
# An analyser reads the compiler's export data through the x/tools its
# release pinned, and a Go patch release can raise that format: Go 1.27.2
# writes version 5, while staticcheck 0.8.1 and gosec 2.29.0 carry an x/tools
# that reads up to 4, and fail on every package ("export data version 5 is
# greater than maximum supported version 4"). `go install <tool>@latest`
# cannot raise a dependency, so the tool is built here in a module of its own
# that can. The toolchain is the job's (GOTOOLCHAIN, from go.mod).
set -euo pipefail
pkg=${1:?usage: build-analyser.sh <package>}
mod=$(mktemp -d)
cd "$mod"
go mod init analyser >/dev/null 2>&1
go get "$pkg@latest" golang.org/x/tools@latest
go build -o "$(go env GOPATH)/bin/$(basename "$pkg")" "$pkg"
