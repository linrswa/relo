#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 /path/to/relo" >&2
  exit 2
fi

relo=$1
mv review-go.mod go.mod
mv review-account.go.txt account.go
mv review-account_test.go.txt account_test.go
"$relo" init --prd review-discovery-prd.md --goal "Deliver account persistence and account creation API with reviewed authorization"
"$relo" task create \
  --title "Implement account persistence" \
  --objective "Persist and retrieve account records through the account store" \
  --accept "TestStoreCreateAndGet passes" \
  --priority 10
"$relo" task create \
  --title "Expose account creation API" \
  --objective "Create accounts through the persistence layer over HTTP" \
  --accept "TestAccountCreationAPI passes" \
  --priority 20
"$relo" task dependency add TASK-002 TASK-001 --reason "The API persists accounts through the completed storage layer"
"$relo" milestone create \
  --title "Account creation integration review" \
  --reason "Review end-to-end account creation before treating the service as stable" \
  --anchor TASK-002 \
  --recommend "Review authorization behavior across the HTTP and persistence boundary"
"$relo" task start TASK-001
go test ./... -run '^TestStoreCreateAndGet$'
"$relo" task pass TASK-001 --summary "TestStoreCreateAndGet passed"
"$relo" task start TASK-002
go test ./... -run '^TestAccountCreationAPI$'
"$relo" task pass TASK-002 --summary "TestAccountCreationAPI passed"
"$relo" validate
