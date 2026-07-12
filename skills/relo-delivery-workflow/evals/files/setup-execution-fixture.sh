#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 /path/to/relo" >&2
  exit 2
fi

relo=$1
mv execution-go.mod go.mod
mv execution-greeting.go.txt greeting.go
mv execution-greeting_test.go.txt greeting_test.go
"$relo" init --prd execution-prd.md --goal "Complete and verify the greeting library"
"$relo" task create \
  --title "Normalize greeting names" \
  --objective "Trim surrounding whitespace from greeting names" \
  --accept "TestNormalizeName passes" \
  --priority 10
"$relo" task create \
  --title "Produce validated greetings" \
  --objective "Return a greeting for normalized names and reject empty names" \
  --accept "TestGreeting passes" \
  --accept "The complete Go test suite passes" \
  --priority 20
"$relo" task dependency add TASK-002 TASK-001 --reason "Greeting output consumes normalized names"
"$relo" milestone create \
  --title "Greeting library stable" \
  --reason "Review complete behavior after normalization and greeting validation pass" \
  --anchor TASK-002 \
  --recommend "Run the complete Go test suite"
"$relo" validate
