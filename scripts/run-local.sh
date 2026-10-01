#!/bin/sh
set -eu

: "${INFRAI_API_KEY:?set INFRAI_API_KEY}"
exec go run ./cmd/commerce-cutover
