# Tasks

## 1. Registry
- [x] 1.1 `wikipedia` settings, defaults, `${VAR}` expansion, validation, bindings, depends_on
- [x] 1.2 Registry tests, `docs/examples/wikipedia.yaml` loads

## 2. Adapter
- [x] 2.1 Claim qualification, wikis, search mode, MediaWiki requests, page selection, passage, matches
- [x] 2.2 On-disk cache, in-run reuse, request accounting
- [x] 2.3 Concurrency, retry, all-or-nothing failure, User-Agent
- [x] 2.4 Offline tests with MediaWiki-shaped fixtures for every shared scenario

## 3. Docs and verification
- [x] 3.1 README section, Dockerfile `DATA_DIR` note
- [x] 3.2 `go test ./...`, `scripts/validate-openspec.sh`
