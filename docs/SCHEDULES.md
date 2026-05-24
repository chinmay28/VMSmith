# Scheduled Operations

VMSmith can fire recurring VM actions — snapshots, start, stop, and restart — on
a cron timer. Schedules are evaluated entirely in-process by the daemon; no
external cron or systemd timer is required.

This document covers the cron-spec syntax, timezone / DST handling, catch-up
policies, retention semantics, and the API / CLI / GUI surfaces.

---

## Quick start

```bash
# Snapshot vm-1741... every night at 02:00 (daemon local time), keeping 7 auto snapshots
vmsmith schedule create \
  --name nightly-snapshot \
  --vm vm-1741234567890123 \
  --action snapshot \
  --cron "0 0 2 * * *" \
  --retention 7

# Stop every VM tagged "dev" at 19:00 on weekdays
vmsmith schedule create \
  --name dev-shutdown \
  --tag dev \
  --action stop \
  --cron "0 0 19 * * 1-5"

vmsmith schedule list
vmsmith schedule show <id>          # definition + recent runs
vmsmith schedule run-now <id>       # fire immediately, out of band of cron
vmsmith schedule edit <id> --enabled=false
vmsmith schedule delete <id>
```

---

## Cron-spec syntax

VMSmith uses [`robfig/cron`](https://pkg.go.dev/github.com/robfig/cron/v3) with
the **6-field, seconds-required** form. This is the *only* accepted form — a
5-field spec is rejected with `invalid_cron_spec` so there is never any
5-vs-6-field ambiguity.

```
┌───────────── second        (0-59)
│ ┌───────────── minute      (0-59)
│ │ ┌───────────── hour      (0-23)
│ │ │ ┌───────────── day of month (1-31)
│ │ │ │ ┌───────────── month  (1-12)
│ │ │ │ │ ┌───────────── day of week (0-6, Sunday = 0)
│ │ │ │ │ │
* * * * * *
```

Examples:

| Spec | Meaning |
|---|---|
| `0 0 2 * * *` | every day at 02:00:00 |
| `0 0 * * * *` | every hour on the hour |
| `0 */15 * * * *` | every 15 minutes |
| `0 0 3 * * 0` | every Sunday at 03:00 |
| `0 0 19 * * 1-5` | 19:00 Monday–Friday |
| `30 0 0 1 * *` | 00:00:30 on the first of every month |

The GUI create/edit form offers preset chips (**Hourly**, **Daily 02:00**,
**Weekly Sun 03:00**) that fill the cron field for the common cases.

---

## Timezones & daylight saving

`timezone` is an IANA name (`America/New_York`, `Europe/Berlin`, …). Leave it
empty to use the daemon host's local timezone.

The scheduler runs **one cron instance per distinct timezone** and routes each
schedule to the right instance, so a schedule's wall-clock time tracks DST
transitions for its zone.

During a fall-back DST transition, an ambiguous local time (e.g. 02:30 on a day
where 02:00–03:00 occurs twice) fires once, matching Go's
`time.ParseInLocation` behavior. Avoid scheduling in the 02:00–03:00 window if
exactly-once firing across DST matters to you.

---

## Targets

A schedule targets VMs one of three ways:

- **`vm_id`** — a single explicit VM.
- **`tag_selector`** — an OR-of-tags match resolved **at fire time** against the
  live VM set. New VMs that match are picked up automatically; deleted VMs
  simply produce zero runs. Each matched VM gets its own run record.
- **neither** — applies to **all** VMs (admin-style fleet schedule).

`vm_id` and `tag_selector` are mutually exclusive (`invalid_target` otherwise).

---

## Catch-up policies

When the daemon is down, fires may be missed. On startup the scheduler compares
the persisted `last_tick` cursor to `now()` and replays missed fires per each
schedule's `catch_up_policy`:

| Policy | Behavior | Use case |
|---|---|---|
| `skip` (default) | Ignore all missed fires; resume normal scheduling. | Idempotent actions where a missed window doesn't matter (periodic snapshot — the next one replaces it). |
| `run_once` | If any fires were missed, run the action exactly once, then resume. | Backups: missing one is OK, but the system should know it's behind. |
| `run_all` | Replay every missed fire in chronological order (capped). | Auditable schedules where every interval matters. |

`run_all` is capped at `max_catch_up` (default 100) replayed fires per schedule
to prevent a replay storm after a long outage. On a fresh install (no
`last_tick` yet) there is no catch-up.

`last_tick` is advanced every `tick_interval_seconds` (default 60) while the
daemon runs.

---

## Actions

| Action | Behavior | Skip conditions |
|---|---|---|
| `snapshot` | Create a snapshot named `auto-<schedule-name>-<timestamp>`. Honors `retention_count`. | `vm_not_found` |
| `start` | Start the VM. | `vm_not_found`, `vm_already_running` |
| `stop` | Graceful stop. | `vm_not_found`, `vm_already_stopped` |
| `restart` | Graceful stop then start. | `vm_not_found` |

### Snapshot retention

When `retention_count > 0`, after each successful snapshot the scheduler lists
snapshots whose names start with `auto-<schedule-name>-` and deletes the oldest
until at most `retention_count` remain. **Only auto-named snapshots from the
same schedule are eligible** — operator-created snapshots are never touched.

This `retention_count` is independent of the daemon-wide
`quotas.max_snapshots_per_vm`; when both apply, the lower effective count wins.

---

## Concurrency, retries & timeouts

- **Per-schedule concurrency** — `max_concurrent` (default 1). If a fire arrives
  while a previous run is still in progress, it is recorded as a `skipped` run
  with `skip_reason: concurrent_run`.
- **Worker pool** — fires dispatch onto a bounded pool
  (`worker_pool_size`, default 4). If the dispatch queue
  (`queue_size`, default 64) is full, the fire is dropped with
  `skip_reason: queue_full` and a `system` event.
- **Retries** — a transient action error is retried up to `max_retries`
  (default 2) with backoff. Retries update the existing run record's `error`
  field rather than creating new run records.
- **Timeout** — each attempt is bounded by `action_timeout_seconds` (default
  300).

---

## Observability

Every fire flows through the event bus with `actor: "scheduler"` (or the API
key alias for `run-now`), distinguishing scheduled runs from operator actions in
the audit log. Emitted event types:

`schedule.created`, `schedule.updated`, `schedule.deleted`, `schedule.fired`,
`schedule.fire_succeeded`, `schedule.fire_failed`, `schedule.fire_skipped`,
`schedule.catch_up_replayed`.

Per-fire outcomes are persisted as **run records** (capped at 200 per schedule),
queryable via `GET /api/v1/schedules/{id}/runs`, `vmsmith schedule show <id>`,
or the GUI's per-row "recent runs" expander.

---

## Configuration

```yaml
schedules:
  enabled: true              # master switch; false -> /schedules returns 503
  worker_pool_size: 4
  queue_size: 64
  max_retries: 2
  action_timeout_seconds: 300
  max_catch_up: 100
  tick_interval_seconds: 60
```

---

## REST API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/schedules` | List (filters: `vm_id`, `action`, `enabled`, `since`/`until` (inclusive `created_at` bounds), `search`; `sort`/`order`/pagination) |
| `POST` | `/api/v1/schedules` | Create |
| `GET` | `/api/v1/schedules/{id}` | Get |
| `PATCH` | `/api/v1/schedules/{id}` | Update (pointer semantics) |
| `DELETE` | `/api/v1/schedules/{id}` | Delete |
| `GET` | `/api/v1/schedules/{id}/runs` | Run history (newest first, paginated) |
| `POST` | `/api/v1/schedules/{id}/run-now` | Fire immediately |

See `docs/openapi.yaml` (or `/api/docs`) for full request/response schemas.
