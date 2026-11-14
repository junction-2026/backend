## 1. Risk package
- [ ] 1.1 Config, defaults, YAML loading, validation, bot mapping (`internal/risk/config.go`)
- [ ] 1.2 Pure computation and texts (`internal/risk/compute.go`, `texts.go`)
- [ ] 1.3 Unit tests for every scenario of the shared spec, config validation, text rules

## 2. Engine, job, stores
- [ ] 2.1 `Job.Risk`, `Clone`; compute in `barrierLocked`; `data.risk`
- [ ] 2.2 `RISK_SCORE_CONFIG` in `cmd/orchestrator`
- [ ] 2.3 Engine tests (order with/without finalizer, cancellation, interruption), store round trip (SQLite and memory)

## 3. Docs
- [ ] 3.1 `config/risk_score/risk_score.yaml`, `docs/examples/bot_detection.yaml` mapping comments, Dockerfile copy
