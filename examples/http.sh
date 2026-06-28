#!/bin/sh
set -eu

addr="${ONCE_ADDR:-127.0.0.1:7410}"

curl -s "http://$addr/v1/reserve" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","command":["send-webhook"]}'
printf '\n'

curl -s "http://$addr/v1/commit" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","state":"succeeded","exit_code":0,"stdout_b64":"UE9TVCAvd2ViaG9vayBldmVudC0xMjMK"}'
printf '\n'

curl -s "http://$addr/v1/reserve" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","command":["send-webhook"]}'
printf '\n'
