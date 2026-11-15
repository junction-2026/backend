## 1. Implementation
- [x] 1.1 `parseFocus` in `internal/httpapi/chat.go` with the shared limits, errors in the same `fields` map
- [x] 1.2 `internal/chat/focus.go`: `FocusItem`, dedupe, `resolve` for every shared kind, `Focus` message with cap
- [x] 1.3 `Service.Reply` takes focus, adds the focus message only with items, logs `focus_count`
- [x] 1.4 `persona.txt` names the focus data as untrusted data

## 2. Tests
- [x] 2.1 Validation table, inclusive limits, combined errors, 422 before 404/409 and not counted by the limiter
- [x] 2.2 Every kind resolves on a fixture job; unknown kind, stale and signed indexes are not found
- [x] 2.3 Dedupe, absent/null/[] focus gives the same upstream messages, injection label stays inside the markers
- [x] 2.4 Focus message cap trims the largest element first; log has `focus_count` and no labels/refs

## 3. Validation
- [x] 3.1 `go test ./... -race -count=1`, `scripts/validate-openspec.sh`
