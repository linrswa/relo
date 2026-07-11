# clipcatalog PRD

Build a small Go command-line application that stores reusable text clips in a JSON file.

## User-facing commands

```text
clipcatalog --file PATH add --text TEXT [--tag TAG ...]
clipcatalog --file PATH list [--tag TAG]
clipcatalog --file PATH search QUERY
clipcatalog --file PATH tag ID --add TAG
```

## Behavior

- A clip has a positive integer ID, non-empty text, zero or more normalized tags, and an RFC3339 creation timestamp.
- Tags are trimmed, lower-cased, deduplicated, and rendered in lexical order.
- `add` assigns `max(existing IDs)+1`; deleted/missing IDs are not reused (there is no delete command in this scope).
- `list` renders clips by ID ascending. `--tag` filters by normalized exact tag.
- `search` is case-insensitive over clip text and tags and renders by ID ascending.
- `tag ID --add TAG` adds one normalized tag. Missing IDs return a clear not-found error and do not rewrite the file.
- The JSON file contains a versioned top-level object and a clips array. A missing file means an empty catalog.
- Writes must be atomic using a temporary file plus rename in the destination directory. A failed write must not damage the previous file.
- All commands write normal output to stdout, diagnostics to stderr, and return non-zero on invalid input/storage errors.
- Use only the Go standard library.

## Quality expectations

- Keep domain validation, persistence, application services, and CLI wiring separable enough to test.
- Unit tests cover normalization, ordering, ID allocation, search, tagging, malformed JSON, and write failure behavior.
- An end-to-end test exercises add → list/filter → search → tag and process restart persistence.
- Include a short README with examples and the JSON format.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `gofmt` must pass.
