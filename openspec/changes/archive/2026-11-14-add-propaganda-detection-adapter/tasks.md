# Tasks

## 1. Registry
- [x] 1.1 `AdapterPropagandaDetection` constant, method/handler/poll_interval/bindings checks, endpoint required
- [x] 1.2 Registry tests (rejections, loading, docs example)

## 2. Adapter
- [x] 2.1 `internal/adapter/propaganda.go` and dispatch in `Execute`
- [x] 2.2 Adapter tests with an httptest keyword mock (request, verbatim result, invalid responses, non-2xx, 3xx, connection error, timeout, cancel, no URL in messages)
- [x] 2.3 Engine-level test: job reaches `succeeded` with the stored result

## 3. Docs
- [x] 3.1 `docs/examples/propaganda_detection.yaml`
- [x] 3.2 README: `PROPAGANDA_DETECTION_URL` and how to add the example

## 4. Verification
- [x] 4.1 `go test -race ./...`
- [x] 4.2 `scripts/validate-openspec.sh`
