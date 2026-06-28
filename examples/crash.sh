#!/bin/sh
set -eu

db="${ONCE_DB:-/tmp/once-demo.db}"
rm -f "$db"

go run ./cmd/once --store "$db" run --key demo -- sh -c 'echo doing-work; exit 0'
go run ./cmd/once --store "$db" run --key demo -- sh -c 'echo this-will-not-run'
go run ./cmd/once --store "$db" get demo
