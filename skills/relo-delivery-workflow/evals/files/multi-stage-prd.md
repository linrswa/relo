# Catalog delivery PRD

Build a catalog service in three layers:

1. Add a SQLite schema and migration for catalog items.
2. Add a repository that persists and retrieves catalog items through that schema.
3. Add an HTTP API that creates and reads items through the repository.

The schema must exist before the repository is implemented, and the repository must exist before the API is implemented. Each layer needs focused tests. Before calling the catalog foundation stable, run an integration test that exercises the HTTP API through the real repository and migrated SQLite database, and review migration rollback behavior.
