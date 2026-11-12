#!/bin/sh
# End-to-end smoke test against a TEST workspace. Needs CONNECT_API_KEY in the environment
# (never passed as an argument) and optionally CONNECT_API_URL. Run by hand; CI does not run this.
set -eu
: "${CONNECT_API_KEY:?set CONNECT_API_KEY in the environment}"
connect=${CONNECT_BIN:-connect}
sender=${SMOKE_SENDER:-Connect}

$connect workspace show
$connect workspace show --json | grep -q '"live": false' || { echo "refusing to run against a live workspace" >&2; exit 1; }

id=$($connect sms send --to +447700900003 --from "$sender" --content "smoke $(date +%s)")
echo "sent $id"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if $connect sms simulator phones --json | grep -q '"direction": "inbound"'; then echo "reply received"; break; fi
  sleep 2
done
$connect sms get "$id" >/dev/null

cid=$($connect contacts create --phone +447700900001 --first-name Smoke --tag smoke)
$connect contacts get "$cid" >/dev/null
$connect contacts delete "$cid"
echo "smoke OK"
