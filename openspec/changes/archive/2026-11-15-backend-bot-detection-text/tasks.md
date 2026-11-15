## 1. Registry
- [x] 1.1 `AdapterBotDetection`, endpoint required, POST only, no handler/poll_interval, bindings only `content: $input`
- [x] 1.2 registry tests

## 2. Adapter
- [x] 2.1 `internal/adapter/bot_detection.go`: request, response forms, label mapping, errors
- [x] 2.2 adapter unit tests (stub detector)
- [x] 2.3 engine test: original text sent although translation succeeded; runs when translation failed

## 3. Docs
- [x] 3.1 `docs/examples/bot_detection.yaml`, README section, risk-config comment
- [x] 3.2 `scripts/validate-openspec.sh`, `go test ./...`
