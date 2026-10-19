#!/bin/sh
# Opt-in only. Configuration is checked inside the suite without printing values.
set -eu
cd "$(dirname "$0")/../../.."
exec go test -count=1 -tags 'stdjson integration' -timeout 4m ./tests/integration/oss
