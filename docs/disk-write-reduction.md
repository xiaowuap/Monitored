# Disk write reduction for the v0.1.2 deployment

This patch preserves collection cadence, raw samples, retention periods and
PostgreSQL/WAL durability. It needs no schema migration.

- Minute/hour rollups still recompute their existing late-arrival windows, but
  update an existing row only when one of the six aggregate values changes.
- Discovery stops updating missing sensors after they reach `removed` with at
  least six misses. Rediscovery still resets them to `present` with zero misses.
- Asset parent links are written only when the parent changes.

The local installation was running server 0.1.2 and worker 0.1.1. On 2026-10-13,
its PostgreSQL statistics showed 400 million hour-rollup updates and 267 million
sensor updates since October 9. Of 17,285 sensors, 16,701 were already removed;
some had accumulated over 350,000 missing-discovery updates.

## Validation

Run `go test ./...` and `go vet ./...`. The regression test
`TestDiskWritesPostgres` requires `MONITORED_TEST_DATABASE_URL` pointing at an
isolated, trust-authenticated PostgreSQL instance with CREATEDB permission. It
creates and removes its own database and checks physical tuple identities across
repeated maintenance, late-arrival aggregate values, stable removed sensors and
rediscovery. Never disable authentication on a production database for testing.

## Local rollout and rollback

Back up the active Compose configuration and database. Build server/worker from
this branch, keep the existing runtime image dependencies, and replace only
those two containers using a Compose override. Leave Agent, web and database
settings unchanged. Keep the previous images and configuration for rollback.

Compare deltas of `pg_stat_wal.wal_bytes` and `pg_stat_user_tables.n_tup_upd`
over several complete worker cycles, excluding builds and backup activity.
WAL bytes measure database log generation, not physical device writes; the host
also runs other applications. Conditional UPSERT still takes row locks, and the
existing aggregation scans and late-arrival windows remain unchanged.
