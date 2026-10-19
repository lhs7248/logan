#!/bin/sh
set -eu
cd "$(dirname "$0")"
mkdir -p output/bin
go build -tags stdjson -trimpath -o output/bin/logan-server .
cp script/bootstrap.sh output/bootstrap.sh
chmod +x output/bootstrap.sh
