#!/bin/sh
set -eu

db="${ONCE_DB:-/tmp/once-webhook.db}"
rm -f "$db"

go run ./cmd/once --store "$db" run --key webhook:event-123 -- sh -c 'echo POST /webhook event-123'
go run ./cmd/once --store "$db" run --key webhook:event-123 -- sh -c 'echo duplicate delivery'
