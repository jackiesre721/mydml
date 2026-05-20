# mydml

[![Go Reference](https://pkg.go.dev/badge/github.com/jackiesre721/mydml.svg)](https://pkg.go.dev/github.com/jackiesre721/mydml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jackiesre721/mydml)](https://goreportcard.com/report/github.com/jackiesre721/mydml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![CI](https://github.com/jackiesre721/mydml/actions/workflows/ci.yml/badge.svg)](https://github.com/jackiesre721/mydml/actions/workflows/ci.yml)

**MySQL large-scale DML tool — batch DELETE / UPDATE / INSERT_SELECT without locking.**

Splits a single DML statement into small batches executed by primary key range, with throttling and replication lag control. Designed for online tables with millions or billions of rows.

---

## Why mydml

When you need to delete, update, or migrate large amounts of data from a MySQL table, a single `DELETE` or `UPDATE` can:

- Produce huge binlog events that exceed `max_binlog_cache_size` and fail
- Lock rows for too long, blocking business queries
- Cause replication lag that cascades to read replicas

Manually writing batch scripts is error-prone — wrong batch methods (e.g. `LIMIT offset`) can still lock tables, and uncontrolled batch frequency causes replica lag spikes.

**mydml solves this by:**

- Splitting DML into small batches by **primary key range** (not OFFSET)
- Sleeping between batches with **adaptive throttling** (replication lag, server load, custom query)
- Supporting **dry-run**, **max-rows limit**, **pause/resume/stop** via HTTP API
- Pre-flight checks: binlog format, foreign keys, triggers

---

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

---

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

---

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

---

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

---

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

---

## Requirements

- MySQL 5.7+ or compatible (MariaDB, TiDB, etc.)
- Target table must have a **single-column integer primary key** (BIGINT, INT, etc.)
- `binlog_format` must be ROW or MIXED (STATEMENT is rejected)
- Table must not be referenced by foreign keys from child tables

---

## Limitations

- Composite primary keys are not supported
- Non-integer (VARCHAR, DATETIME) primary keys are not supported
- No checkpoint/resume — if interrupted, the tool restarts from the beginning (but already-processed rows are simply skipped since they no longer match the WHERE condition)
- Control API has no authentication (binds to localhost by default)

---

## License

MIT

---

<br/>
<br/>

---

# mydml

**MySQL 大规模 DML 工具 —— 分批 DELETE / UPDATE / INSERT_SELECT，不锁表。**

按主键范围将一条 DML 拆分为小批次执行，内置限流和主备延迟控制。适用于百万到百亿行级的在线表。

---

## 为什么需要 mydml

当需要从 MySQL 表中删除、更新或迁移大量数据时，单条 `DELETE` 或 `UPDATE` 可能：

- 产生巨大的 binlog 事件，超过 `max_binlog_cache_size` 导致失败
- 长时间锁行，阻塞业务查询
- 造成主备延迟，影响读库

手动写分批脚本容易出错——错误的分批方式（如 `LIMIT offset`）仍然会锁表，分批频率控制不当会造成主备延迟飙升。

**mydml 的解决方式：**

- 按**主键范围**拆分 DML 为小批次（非 OFFSET）
- 批次间自动 **Sleep 限流**（根据主备延迟、服务器负载、自定义查询自适应调节）
- 支持 **dry-run 预跑**、**max-rows 行数限制**、通过 HTTP API **暂停/恢复/停止**
- 预检查：binlog 格式、外键约束、触发器

---

## 安装

```bash
go install github.com/jackiesre721/mydml/cmd/mydml@latest
```

或从源码构建：

```bash
git clone https://github.com/jackiesre721/mydml.git
cd mydml
make build
```

需要 Go 1.22+。

---

## 快速开始

### 删除数据

```bash
mydml delete \
  --host=127.0.0.1 --port=3306 \
  --user=root --password=secret \
  --database=mydb \
  --table=orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --batch-size=500 --sleep-ms=100
```

### 更新数据

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

### 数据迁移（INSERT_SELECT）

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

### 预跑模式（只统计，不执行）

```bash
mydml delete \
  --host=127.0.0.1 --user=root --database=mydb \
  --table=orders \
  --where="status = 'expired'" \
  --dry-run
```

---

## 全部参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--host` | `127.0.0.1` | MySQL 主机 |
| `--port` | `3306` | MySQL 端口 |
| `--user` | | MySQL 用户（必填） |
| `--password` | | MySQL 密码 |
| `--database` | | MySQL 数据库（必填） |
| `--table` | | 目标表名（delete/update 必填） |
| `--where` | | WHERE 条件，不含 WHERE 关键字（必填） |
| `--set` | | UPDATE 的 SET 子句（update 模式必填） |
| `--source-table` | | 源表名（insert-select 必填） |
| `--target-table` | | 目标表名（insert-select 必填） |
| `--columns` | | insert-select 的列列表（默认 `*`） |
| `--batch-size` | `500` | 每批行数（100–5000） |
| `--sleep-ms` | `100` | 批次间基础 Sleep 时间（毫秒） |
| `--max-lag-sec` | `1` | 主备延迟阈值（秒） |
| `--nice-ratio` | `0` | 工作:睡眠比例（0=固定 sleep，>0=按比例） |
| `--max-load` | | 负载阈值，如 `Threads_running=25` |
| `--critical-load` | | 临界负载阈值（超过即停止任务） |
| `--throttle-query` | | 自定义限流 SQL（返回值 > 0 触发限流） |
| `--check-slave-lag` | | 备库 `host:port`（可重复指定多个） |
| `--dry-run` | `false` | 预跑模式（只统计，不改数据） |
| `--max-rows` | `0` | 最大影响行数（0=不限） |
| `--control-addr` | `127.0.0.1:8080` | HTTP 控制 API 地址 |
| `--task-id` | | 自定义任务 ID（不填则自动生成） |
| `--verbose` | `false` | 详细日志 |
| `--log-file` | | 日志文件路径（默认输出到 stdout） |

环境变量：`MYSQL_DELETE_HOST`、`MYSQL_DELETE_PORT`、`MYSQL_DELETE_USER`、`MYSQL_DELETE_PASSWORD`、`MYSQL_DELETE_DATABASE`、`MYSQL_DELETE_TABLE`。

---

## HTTP 控制 API

任务运行时，可通过 HTTP 控制：

```bash
# 暂停
curl -X POST http://127.0.0.1:8080/api/v1/pause

# 恢复
curl -X POST http://127.0.0.1:8080/api/v1/resume

# 停止（完成当前批次后退出）
curl -X POST http://127.0.0.1:8080/api/v1/stop

# 立即终止
curl -X POST http://127.0.0.1:8080/api/v1/panic

# 查看状态
curl http://127.0.0.1:8080/api/v1/status

# 运行时调整限流参数
curl -X PUT http://127.0.0.1:8080/api/v1/config \
  -d '{"sleep_ms": 200, "nice_ratio": 1.5, "max_lag_sec": 5}'
```

---

## 工作原理

```
1. 校验参数 & 连接 MySQL
2. 预检查（binlog 格式、外键、触发器）
3. 从 information_schema 检测主键列
   - 仅支持单列整数主键
4. 获取主键范围：
   SELECT pk FROM t ORDER BY pk ASC  LIMIT 1   -- 最小值
   SELECT pk FROM t ORDER BY pk DESC LIMIT 1   -- 最大值
5. 按范围分批执行（三种模式）：

   DELETE 模式：
   DELETE FROM t
     WHERE pk >= cursor AND pk < cursor + batch_size
     AND status = 'expired'

   UPDATE 模式：
   UPDATE t SET status = 'archived'
     WHERE pk >= cursor AND pk < cursor + batch_size
     AND status = 'expired'

   INSERT_SELECT 模式：
   INSERT INTO target SELECT * FROM source
     WHERE pk >= cursor AND pk < cursor + batch_size
     AND created_at < '2023-01-01'

6. 限流：根据影响行数、主备延迟、服务器负载、nice-ratio 计算 Sleep
7. 完成后输出摘要报告
```

**核心设计：**

- **主键范围分批**（非 OFFSET）—— 避免全表扫描和锁升级
- **ORDER BY + LIMIT 1** 获取主键范围 —— B+tree 直接定位首尾叶子节点，O(log n)，百亿行表也微秒级返回
- **主键范围即批次大小** —— 每个 chunk 恰好覆盖 batch_size 个主键值，无需 LIMIT
- **自适应限流** —— 主备延迟、锁等待、服务器负载超阈值时自动增加 Sleep 时间

---

## 环境要求

- MySQL 5.7+ 或兼容数据库（MariaDB、TiDB 等）
- 目标表必须有**单列整数主键**（BIGINT、INT 等）
- `binlog_format` 必须为 ROW 或 MIXED（STATEMENT 会被拒绝）
- 表不能被子表外键引用

---

## 限制

- 不支持复合主键
- 不支持非整数类型主键（VARCHAR、DATETIME 等）
- 无断点续跑 —— 中断后需从头开始（但已处理的行不再匹配 WHERE 条件，会被快速跳过）
- 控制 API 无认证（默认绑定 localhost）

---


## License

MIT
