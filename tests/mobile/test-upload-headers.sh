#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
binary=$(mktemp "${TMPDIR:-/tmp}/logan-upload-test.XXXXXX")
trap 'rm -f "$binary"' EXIT
xcrun clang -fobjc-arc -fblocks -Wno-deprecated-declarations -Wno-nullability-completeness -framework Cocoa -I Logan/Clogan tests/mobile/upload_headers.m -o "$binary"
"$binary"
