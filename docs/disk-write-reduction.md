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
those two containers by pinning the image tags in the active Compose file. Leave Agent, web and database
settings unchanged. Keep the previous images and configuration for rollback.

Compare deltas of `pg_stat_wal.wal_bytes` and `pg_stat_user_tables.n_tup_upd`
over several complete worker cycles, excluding builds and backup activity.
WAL bytes measure database log generation, not physical device writes; the host
also runs other applications. Conditional UPSERT still takes row locks, and the
existing aggregation scans and late-arrival windows remain unchanged.

## Local acceptance, 2026-10-13 (Asia/Shanghai)

Deployed code: `a2eb95c`, based on release `7d90cc9` (v0.1.2), in an
isolated worktree. Existing uncommitted development work was preserved.
The active `/home/monitored/compose.release.yaml` pins only these two images:

- `monitored-local/server:disk-a2eb95c`
- `monitored-local/worker:disk-a2eb95c`

The binaries were built with `CGO_ENABLED=0 go build -buildvcs=false -trimpath
-ldflags='-s -w'` using the corresponding `./cmd/server` and `./cmd/worker`
packages. Local runtime images retain the prior server 0.1.2 / worker 0.1.1
base images, replacing only the binaries and adding the revision label. Images
were built locally, not published to a container registry.

| Counter | Before, per second | After, per second | Reduction |
| --- | ---: | ---: | ---: |
| PostgreSQL WAL bytes | 1,207,081 | 375,490 | 68.89% |
| Sensor row updates | 781.85 | 29.79 | 96.19% |
| Minute rollup updates | 203.11 | 0.17 | 99.92% |
| Hour rollup updates | 896.65 | 7.79 | 99.13% |

Before: 10:23:05–10:32:04 (538.84 seconds). After:
10:34:55–10:38:24 (209.83 seconds). WAL counters were
662486981955 → 663137408112 and 663265712753 → 663344503045.
These are short live observation windows, not an equal-load benchmark or a
measurement of all physical disk writes. A logical backup overlapped parts of
both windows; checkpoint timing and other database work affect WAL volume.

Validation completed:

- Full `go test ./...`, including the isolated PostgreSQL regression, passed.
- `go vet ./...` and `git diff --check` passed.
- Server health endpoint reported database healthy; authenticated login,
  overview, assets and agents endpoints returned HTTP 200.
- Agent stayed active; at 10:38:24 the newest raw sample was from 10:38:23.
- Worker completed maintenance at 10:34:59, 10:36:25 and 10:37:13.
- Schema migration ledger stayed at seven entries; no new migration applied.

Backup and rollback assets are in
`/home/monitored/backups/disk-writes-20261013/`. The full custom-format dump
(~799 MiB) completed successfully and `pg_restore --file=/dev/null` read it
successfully; a SHA-256 checksum is stored beside it. This verifies archive
readability, not a full database restore drill. The consistent dump snapshot
started before deployment and finished afterwards. Configuration, old image
references and schema were saved before replacing the application containers.

Run `/home/monitored/backups/disk-writes-20261013/rollback.sh` as root to restore
the original Compose configuration and recreate only server/worker with the
retained old images. No data rollback is required for this patch.
