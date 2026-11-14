# Design: SQLite job store

Shared contract: shared-specs @ b4a04a191450549e9d263166bd8cc2904044cff6, change `persistent-job-history`. Based on
backend @ 2ee817ed318928e5ecb2d6ce9aab822a7cc99f16.

## Decisions

1. **Store = working set + history.** The engine keeps live `*job.Record`s in the store's working set exactly as
   before and additionally calls `Save` with the record while holding its lock. `Memory` implements history as
   no-ops (so the default costs nothing and behaves as before). `SQLite` embeds a `Memory` as working set, so
   `EvictFinished` (JOB_RETENTION) can never touch the database.
2. **Write at every event.** `run.emit` publishes the event, remembers its seq in `Record.LastSeq` and saves the job.
   Every state change of a job is followed by an event in the same locked section, so this covers creation (explicit
   save), every settle, status change and terminal state, and the stored `last_seq` always matches the stored state;
   the terminal write carries the final seq. No coalescing: events are a few dozen per job. The job is serialized
   from `Clone()` under its lock, so a record that is changing is never serialized.
3. **No writes after `Engine.Close`.** Closing cancels every analyzer, which would otherwise settle them as
   `cancelled` and finish the job as completed in the database. After Close nothing is saved, so a job that was
   running at shutdown stays as it was and is ended `interrupted` at the next start.
4. **Schema.** `jobs(id PK, status, done, created_at, finished_at, data, state)`; `created_at`/`finished_at` are unix
   nanoseconds (RFC 3339 text does not sort like time), `data` is the Job JSON, `state` is `{plan, progress,
   previews, activities, last_seq}`. Indexes `(created_at DESC, id)` and `(status, created_at DESC, id)` serve the
   newest-first listing; partial indexes serve startup recovery and history pruning. `schema_version` holds one row;
   migrations are an append-only list applied in one `BEGIN IMMEDIATE` transaction.
5. **Lossless.** `Result` members are `json.RawMessage`, and previews are read back as raw JSON, so reading and
   marshalling again gives equivalent JSON (Go's `Marshal` may escape `&`, `<`, `>`, which is equivalent JSON). A
   stored `null` result reads back as nil.
6. **Listing.** `Store.Page(status, after, limit)` returns at most `limit` jobs; `GET /jobs` asks for `limit+1`
   (the extra one tells whether there is a next page). SQLite decodes only the rows it returns.
7. **Recovery.** `Engine.Recover` (called by `main` before the listener opens) loads `done = 0` jobs, applies
   `Record.Interrupt(now)` and saves them. No event is emitted and the bus is not touched.
8. **Persisted streams.** A job that is not in the working set is read with `Load`; `Subscribe` answers with
   `Bus.Closed(id, last_seq, snapshot)`, a one-event closed subscription that leaves no state in the bus.
9. **Retention.** `Sweep` evicts from memory after `JOB_RETENTION`; with `JOB_HISTORY_RETENTION` it also deletes done
   jobs finished before it from the database and evicts from memory after the smaller of the two.
10. **Write serialization.** One mutex around writes; reads use the pool (WAL), busy timeout 5 s. Write failures are
    logged (`Config.Warn`) and never fail a job.

## Risks

- A write per event while the job lock is held: bounded by the job's own event rate; acceptable for SQLite in WAL
  mode with `synchronous=NORMAL`.
- Previews are stored as published; nothing else is derived on read.
