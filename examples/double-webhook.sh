#!/bin/sh
set -eu

db="${ONCE_DB:-/tmp/once-double-webhook.db}"
log="${ONCE_LOG:-/tmp/once-double-webhook.log}"
rm -f "$db" "$log"

side_effect='printf "webhook fired\n" >> "$ONCE_LOG"; echo ok'

echo "without once:"
ONCE_LOG="$log" sh -c "$side_effect"
ONCE_LOG="$log" sh -c "$side_effect"
wc -l < "$log"

rm -f "$log"

echo "with once:"
ONCE_LOG="$log" go run ./cmd/once --store "$db" run --key webhook:event-123 -- sh -c "$side_effect"
ONCE_LOG="$log" go run ./cmd/once --store "$db" run --key webhook:event-123 -- sh -c "$side_effect"
wc -l < "$log"
