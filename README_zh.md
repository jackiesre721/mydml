# mydml

[![Go Reference](https://pkg.go.dev/badge/github.com/jackiesre721/mydml.svg)](https://pkg.go.dev/github.com/jackiesre721/mydml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jackiesre721/mydml)](https://goreportcard.com/report/github.com/jackiesre721/mydml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![CI](https://github.com/jackiesre721/mydml/actions/workflows/ci.yml/badge.svg)](https://github.com/jackiesre721/mydml/actions/workflows/ci.yml)

**MySQL 大规模 DML 工具 —— 分批 DELETE / UPDATE / INSERT_SELECT，不锁表。**

[English](README.md)

---

## 特性

- 按**主键范围**拆分 DML 为小批次（非 OFFSET）
- 三种模式：**DELETE**、**UPDATE**、**INSERT_SELECT**
- **自适应限流** — 主备延迟、服务器负载、自定义查询
- **HTTP 控制 API** — 运行时暂停 / 恢复 / 停止
- **dry-run 预跑**模式和 **max-rows** 行数限制
- 预检查：binlog 格式、外键约束、触发器
- 单 Go 二进制文件，无外部依赖

## 安装

**一键安装（macOS / Linux）：**

```bash
curl -fsSL https://github.com/jackiesre721/mydml/raw/main/install.sh | bash
```

**macOS (Homebrew)：**

```bash
brew install jackiesre721/tap/mydml
```

**Linux (deb/rpm)：**

```bash
# Debian/Ubuntu
wget https://github.com/jackiesre721/mydml/releases/latest/download/mydml_amd64.deb
sudo dpkg -i mydml_amd64.deb

# RHEL/CentOS/Fedora
sudo rpm -i https://github.com/jackiesre721/mydml/releases/latest/download/mydml_amd64.rpm
```

**Go install：**

```bash
go install github.com/jackiesre721/mydml/cmd/mydml@latest
```

或从源码构建：

```bash
git clone https://github.com/jackiesre721/mydml.git
cd mydml
make build
```

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

## 环境要求

- MySQL 5.7+ 或兼容数据库（MariaDB、TiDB 等）
- 目标表必须有**单列整数主键**（BIGINT、INT 等）
- `binlog_format` 必须为 ROW 或 MIXED（STATEMENT 会被拒绝）
- 表不能被子表外键引用

## 限制

- 不支持复合主键
- 不支持非整数类型主键（VARCHAR、DATETIME 等）
- 无断点续跑 —— 中断后需从头开始（但已处理的行不再匹配 WHERE 条件，会被快速跳过）
- 控制 API 无认证（默认绑定 localhost）

## License

[MIT](LICENSE)
