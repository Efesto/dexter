#!/usr/bin/env bash

set -euo pipefail

GL_REPO=git@gitlab.com:remote-com/employ-starbase/dexter
TOOL_NAME="dexter"
TOOL_TEST="dexter --help"

fail() {
	echo -e "asdf-$TOOL_NAME: $*"
	exit 1
}

sort_versions() {
	sed 'h; s/[+-]/./g; s/.p\([[:digit:]]\)/.z\1/; s/$/.z/; G; s/\n/ /' |
		LC_ALL=C sort -t. -k 1,1 -k 2,2n -k 3,3n -k 4,4n -k 5,5n | awk '{print $2}'
}

list_all_versions() {
	git ls-remote --tags --refs "$GL_REPO" |
		grep -o 'refs/tags/.*' | cut -d/ -f3- |
		sed 's/^v//' || fail "Could not list versions. Ensure you have added your ssh key to gitlab."
}

download_release() {
	local version="$1"
	local download_path="$2"

	echo "* Cloning $TOOL_NAME v$version..."
	git clone --depth 1 --branch "v${version}" "${GL_REPO}.git" "$download_path" 2>/dev/null ||
		git clone --depth 1 --branch "${version}" "${GL_REPO}.git" "$download_path" ||
		fail "Could not clone $GL_REPO at version $version"
}

install_version() {
	local install_type="$1"
	local version="$2"
	local install_path="${3%/bin}/bin"

	if [ "$install_type" != "version" ]; then
		fail "asdf-$TOOL_NAME supports release installs only"
	fi

	(
		mkdir -p "$install_path"

		command -v go >/dev/null 2>&1 || fail "Go is required to build dexter. Install it from https://go.dev/dl/"
		command -v cc >/dev/null 2>&1 || fail "A C compiler is required (for SQLite). On macOS run: xcode-select --install"

		echo "* Building $TOOL_NAME v$version..."
		cd "$ASDF_DOWNLOAD_PATH"
		CGO_ENABLED=1 go build -o "$install_path/$TOOL_NAME" ./cmd/

		local tool_cmd
		tool_cmd="$(echo "$TOOL_TEST" | cut -d' ' -f1)"
		test -x "$install_path/$tool_cmd" || fail "Expected $install_path/$tool_cmd to be executable."

		echo "$TOOL_NAME $version installation was successful!"
	) || (
		rm -rf "$install_path"
		fail "An error occurred while installing $TOOL_NAME $version."
	)
}
