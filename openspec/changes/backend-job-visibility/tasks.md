## 1. Implementation
- [x] 1.1 `job.Job.Public`, `Clone`, `UnmarshalJSON` default
- [x] 1.2 `engine.CreateRequest.Private`, `Create` sets `Public`
- [x] 1.3 Handler parses and validates `public`
- [x] 1.4 SQLite migration, `Save` column, `Page` filter; memory `Page` filter

## 2. Tests
- [x] 2.1 Handler tests: default, true, false, invalid values on input and scrape bodies; list exclusion with paging
- [x] 2.2 Store tests: filter with and without status, interleaved cursor paging in SQLite and memory, migration of an old-schema database
- [x] 2.3 Engine tests: get, cancel, events for an unlisted job; restart keeps it unlisted
