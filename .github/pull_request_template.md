## What this changes

<!-- A short description of the change and why it is needed. Link the issue if there is one. -->

## Checklist

- [ ] `go build ./...`, `go vet ./...` and `go test ./...` pass
- [ ] `gofmt -l .` prints nothing
- [ ] TUI golden files, if any changed, were updated on purpose with `go test ./internal/tui -run TestGolden -update`, and the diff was read
- [ ] No credentials, tokens, account identifiers, usernames, hostnames or other personal data in fixtures, recordings, golden files or this description
- [ ] Docs are updated where behavior changed (README, CONTRIBUTING, or the in-app help)
- [ ] The change keeps the rules in CONTRIBUTING.md ("Playing by the rules"): no credential reads, no direct model API calls, no self-approval, and `internal/guard` still passes without changes to weaken it

## Notes for the reviewer

<!-- Anything that needs a closer look: a design choice, a golden change, a trade-off. -->
