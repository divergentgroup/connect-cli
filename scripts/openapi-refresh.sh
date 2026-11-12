#!/bin/sh
# Downloads the Connect v2 public document into openapi/public-v2.json. Single-entry oneOf wrappers
# around a $ref are unwrapped so oapi-codegen emits plain structs rather than opaque unions.
# Then run `go generate ./...`.
set -eu
url=${CONNECT_API_URL:-https://api.connect.ms}
curl -fsSL "$url/openapi/public-v2.json" | jq 'walk(if type == "object" and (.oneOf | type) == "array" and (.oneOf | length) == 1 and (.oneOf[0] | has("$ref")) then .oneOf[0] + (if .description then {description} else {} end) else . end)' > openapi/public-v2.json
