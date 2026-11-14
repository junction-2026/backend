# Design: job visibility

- `job.Job.Public bool` (`json:"public"`). `Job.UnmarshalJSON` starts from `Public: true`, so stored JSON without the member reads as public; this is the only decode path (SQLite `decode` and `Page`).
- `engine.CreateRequest.Private` (zero value = public) keeps `CreateRequest{}` literals public; `Create` sets `Public: !Private`. `Clone` copies the field, so REST, events and storage agree.
- The handler reads `top["public"]` raw: `null` and non-booleans are recorded under `public` next to the other violations, so no job is created.
- SQLite migration 2: `ALTER TABLE jobs ADD COLUMN public INTEGER NOT NULL DEFAULT 1` (existing rows become public; the schema version makes it run once) and indexes `(public, created_at DESC, id)` and `(public, status, created_at DESC, id)`. `Save` writes the column from the record. `Page` always has `public = 1` in its WHERE, so the limit and cursor count public jobs only.
- The `data` JSON of rows written before the migration has no `public`; reading it applies the default.
- `JobSummary` is unchanged (every listed item is public).
