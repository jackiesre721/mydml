# mydml

[![Go Reference](https://pkg.go.dev/badge/github.com/jackiesre721/mydml.svg)](https://pkg.go.dev/github.com/jackiesre721/mydml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jackiesre721/mydml)](https://goreportcard.com/report/github.com/jackiesre721/mydml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![CI](https://github.com/jackiesre721/mydml/actions/workflows/ci.yml/badge.svg)](https://github.com/jackiesre721/mydml/actions/workflows/ci.yml)

**MySQL large-scale DML tool — batch DELETE / UPDATE / INSERT_SELECT without locking.**

[中文文档](README_zh.md)

---

## Features

- Split DML into small batches by **primary key range** (not OFFSET)
- Three modes: **DELETE**, **UPDATE**, **INSERT_SELECT**
- **Adaptive throttling** — replication lag, server load, custom query
- **HTTP control API** — pause / resume / stop at runtime
- **Dry-run** mode and **max-rows** limit
- Pre-flight checks: binlog format, foreign keys, triggers
- Single Go binary, no external dependencies

## Install

```bash
go install github.com/jackiesre721/mydml/cmd/mydml@latest
```

Or build from source:

```bash
git clone https://github.com/jackiesre721/mydml.git
cd mydml
make build
```

Requires Go 1.22+.

## Quick Start

### Delete rows

```bash
mydml delete \
  --host=127.0.0.1 --port=3306 \
  --user=root --password=secret \
  --database=mydb \
  --table=orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --batch-size=500 --sleep-ms=100
```

### Update rows

```bash
mydml update \
  --host=127.0.0.1 --port=3306 \
  --user=root --password=secret \
  --database=mydb \
  --table=orders \
  --where="status = 'pending' AND created_at < '2023-01-01'" \
  --set="status = 'archived'" \
  --batch-size=500 --sleep-ms=100
```

### Insert-select (data migration)

```bash
mydml insert-select \
  --host=127.0.0.1 --port=3306 \
  --user=root --password=secret \
  --database=mydb \
  --source-table=orders \
  --target-table=orders_archive \
  --where="created_at < '2023-01-01'" \
  --batch-size=500 --sleep-ms=100
```

### Dry-run (count only, no changes)

```bash
mydml delete \
  --host=127.0.0.1 --user=root --database=mydb \
  --table=orders \
  --where="status = 'expired'" \
  --dry-run
```

## All Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | `127.0.0.1` | MySQL host |
| `--port` | `3306` | MySQL port |
| `--user` | | MySQL user (required) |
| `--password` | | MySQL password |
| `--database` | | MySQL database (required) |
| `--table` | | Target table (required for delete/update) |
| `--where` | | WHERE condition without the `WHERE` keyword (required) |
| `--set` | | SET clause for update mode (required for update) |
| `--source-table` | | Source table for insert-select (required) |
| `--target-table` | | Target table for insert-select (required) |
| `--columns` | | Column list for insert-select (default: `*`) |
| `--batch-size` | `500` | Rows per batch (100–5000) |
| `--sleep-ms` | `100` | Base sleep between batches in ms |
| `--max-lag-sec` | `1` | Max replication lag threshold in seconds |
| `--nice-ratio` | `0` | Work:sleep ratio (0 = fixed sleep, >0 = proportional) |
| `--max-load` | | Load thresholds, e.g. `Threads_running=25` |
| `--critical-load` | | Critical load thresholds (stops task when exceeded) |
| `--throttle-query` | | Custom SQL for throttle check (value > 0 triggers throttle) |
| `--check-slave-lag` | | Replica `host:port` for lag checking (repeatable) |
| `--dry-run` | `false` | Dry-run mode (count only, no data changes) |
| `--max-rows` | `0` | Max rows to affect (0 = unlimited) |
| `--control-addr` | `127.0.0.1:8080` | HTTP control API address |
| `--task-id` | | Custom task ID (auto-generated if empty) |
| `--verbose` | `false` | Verbose logging |
| `--log-file` | | Log file path (default: stdout) |

Environment variables: `MYSQL_DELETE_HOST`, `MYSQL_DELETE_PORT`, `MYSQL_DELETE_USER`, `MYSQL_DELETE_PASSWORD`, `MYSQL_DELETE_DATABASE`, `MYSQL_DELETE_TABLE`.

## HTTP Control API

While a task is running, you can control it via HTTP:

```bash
# Pause
curl -X POST http://127.0.0.1:8080/api/v1/pause

# Resume
curl -X POST http://127.0.0.1:8080/api/v1/resume

# Stop (completes current batch, then exits)
curl -X POST http://127.0.0.1:8080/api/v1/stop

# Immediate termination
curl -X POST http://127.0.0.1:8080/api/v1/panic

# Status
curl http://127.0.0.1:8080/api/v1/status

# Adjust throttle at runtime
curl -X PUT http://127.0.0.1:8080/api/v1/config \
  -d '{"sleep_ms": 200, "nice_ratio": 1.5, "max_lag_sec": 5}'
```

## How It Works

```
1. Validate config & connect to MySQL
2. Pre-flight checks (binlog format, foreign keys, triggers)
3. Detect PK column from information_schema
   - Must be single-column integer PK
4. Get PK range:
   SELECT pk FROM t ORDER BY pk ASC  LIMIT 1   -- min
   SELECT pk FROM t ORDER BY pk DESC LIMIT 1   -- max
5. Execute in batches (one of three modes):

   DELETE mode:
   DELETE FROM t
     WHERE pk >= cursor AND pk < cursor + batch_size
     AND status = 'expired'

   UPDATE mode:
   UPDATE t SET status = 'archived'
     WHERE pk >= cursor AND pk < cursor + batch_size
     AND status = 'expired'

   INSERT_SELECT mode:
   INSERT INTO target SELECT * FROM source
     WHERE pk >= cursor AND pk < cursor + batch_size
     AND created_at < '2023-01-01'

6. Throttle: sleep based on affected rows, replication lag,
   server load, and nice-ratio
7. Report summary when done
```

**Key design choices:**

- **PK range batching** (not OFFSET) — avoids table scan and lock escalation
- **ORDER BY + LIMIT 1** for PK range — B+tree direct leaf access, O(log n), fast on billion-row tables
- **PK range constrains batch size** — each chunk covers exactly batch_size PK values, no LIMIT needed
- **Adaptive throttle** — increases sleep when replication lag, lock waits, or server load exceeds thresholds

## Comparison

| Feature | mydml | pt-archiver | gh-ost |
|---------|-------|-------------|--------|
| Batch DELETE | Yes | Yes | No |
| Batch UPDATE | Yes | Limited | No |
| Batch INSERT_SELECT | Yes | No | No |
| Replication lag control | Yes | Yes | Yes |
| Load-aware throttle | Yes | Limited | No |
| HTTP pause/resume/stop | Yes | No | Yes |
| Dry-run mode | Yes | No | No |
| No external dependencies | Yes (single binary) | No (Perl) | No (requires binlog) |
| Online DDL | No | No | Yes |

## Requirements

- MySQL 5.7+ or compatible (MariaDB, TiDB, etc.)
- Target table must have a **single-column integer primary key** (BIGINT, INT, etc.)
- `binlog_format` must be ROW or MIXED (STATEMENT is rejected)
- Table must not be referenced by foreign keys from child tables

## Limitations

- Composite primary keys are not supported
- Non-integer (VARCHAR, DATETIME) primary keys are not supported
- No checkpoint/resume — if interrupted, the tool restarts from the beginning (but already-processed rows are simply skipped since they no longer match the WHERE condition)
- Control API has no authentication (binds to localhost by default)

## License

[MIT](LICENSE)
