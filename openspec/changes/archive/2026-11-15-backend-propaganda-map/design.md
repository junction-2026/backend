# Design

- Map columns live on the `jobs` row (`map_eligible` NULL until determined, `map_country_code`, `map_country`, `map_outlet`, `map_techniques`),
  so retention (`DELETE FROM jobs`) removes map data with the job: no orphan rows. A partial index
  `jobs_map (map_country_code, created_at DESC, id) WHERE map_eligible = 1` serves the list and the aggregate.
  The id tie-break is ascending, the same as `GET /jobs` ("ties by id"), so the index sorts the list.
- `store.DeriveMap(job, outlets.Table)` is the single derivation; `SQLite.Save` calls it when `done` is true and writes the values with the
  done record; a conflict update keeps values already determined (`COALESCE`/`CASE`), so later saves and outlet-table changes never move a job.
- The outlet table is handed to the store by `engine.New` (the table of the enabled `source_info` analyzer) through `Store.SetOutlets`.
- `Store.BackfillMap` (called by `Engine.Recover`) derives values for done rows with `map_eligible IS NULL`, 200 per transaction; a failure is
  logged and retried at the next start. A stored job that cannot be decoded is recorded as not eligible.
- The Memory store derives at `Save` of a done job and keeps the values next to the working set until eviction.
- The aggregate is two grouped queries (counts with the newest country name; technique counts through `json_each`); both stores share the
  sorting (`mapAgg`). Handlers build MapAnalysis from the job and the stored values, snippet and verdict at read time.
