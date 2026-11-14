# Tasks

## 1. Outlet table
- [x] 1.1 `internal/outlets`: loader (header, row validation, lookup maps), host normalization, matching
- [x] 1.2 Tests: 5-row matching, three validation cases, real `data/outlets.csv`

## 2. Registry
- [x] 2.1 Adapter constant, `execution.outlets_file`, key/binding/trigger/dependency validation
- [x] 2.2 Load the table for enabled analyzers once per file; startup errors name analyzer and file
- [x] 2.3 Tests

## 3. Adapter
- [x] 3.1 `source_info` adapter: articles, `page_outlet`, summary, table, `no_sources` skip, failures
- [x] 3.2 Tests: 12-article summary, page outlet, skip, unparsable URL, malformed result, cancellation

## 4. Packaging and docs
- [x] 4.1 Dockerfile `COPY data/outlets.csv`
- [x] 4.2 `docs/examples/source_info.yaml`, README section
