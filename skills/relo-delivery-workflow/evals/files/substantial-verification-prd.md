# Checkout delivery PRD

Deliver checkout in two implementation layers:

1. Add a pricing library that calculates item totals and discounts.
2. Add a checkout HTTP API that uses the pricing library and persists completed checkouts.

Each implementation layer needs focused unit tests for its local behavior. Those tests use the same package boundaries and require no special environment.

Before checkout can be considered release-ready, build a new end-to-end verification harness that starts the real checkout API, PostgreSQL, and a controllable payment-provider stub; seeds isolated fixtures; exercises successful and declined payments; verifies persisted checkout state; cleans up reliably; and runs in CI. The harness and CI environment do not exist yet and will be maintained as a distinct testing asset.
