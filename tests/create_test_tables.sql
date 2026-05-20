-- ============================================================
-- Integration Test Data Setup for mysql-delete-tools
-- Run: mysql -u root delete_test < tests/create_test_tables.sql
-- ============================================================

SET FOREIGN_KEY_CHECKS=0;

-- Helper: number sequence table (0..9999) for data generation
DROP PROCEDURE IF EXISTS _generate_numbers;
DELIMITER //
CREATE PROCEDURE _generate_numbers()
BEGIN
  DROP TEMPORARY TABLE IF EXISTS _nums;
  CREATE TEMPORARY TABLE _nums (n INT PRIMARY KEY);
  INSERT INTO _nums (n)
  SELECT (a.n * 1000 + b.n * 100 + c.n * 10 + d.n) AS n
  FROM
    (SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9) a,
    (SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9) b,
    (SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9) c,
    (SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9) d;
END //
DELIMITER ;

CALL _generate_numbers();

-- ============================================================
-- Table 1: t_orders — Happy Path (INT PK + indexed WHERE)
-- 10000 rows, ~3000 expired+old
-- ============================================================
DROP TABLE IF EXISTS t_orders;
CREATE TABLE t_orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id INT NOT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'pending',
  amount DECIMAL(10,2) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status (status),
  KEY idx_created (created_at)
) ENGINE=InnoDB;

INSERT INTO t_orders (user_id, status, amount, created_at)
SELECT
  n AS user_id,
  CASE
    WHEN n % 10 IN (0,1,2) THEN 'expired'
    WHEN n % 10 IN (3,4)   THEN 'cancelled'
    ELSE 'active'
  END AS status,
  ROUND(50 + (n % 500), 2) AS amount,
  CASE
    WHEN n % 10 IN (0,1,2) THEN DATE_ADD('2023-01-01', INTERVAL (n % 365) DAY)
    ELSE DATE_ADD('2025-01-01', INTERVAL (n % 365) DAY)
  END AS created_at
FROM _nums
WHERE n < 10000;

-- ============================================================
-- Table 2: t_logs — No Index on WHERE Column
-- 5000 rows, ~500 legacy_import
-- ============================================================
DROP TABLE IF EXISTS t_logs;
CREATE TABLE t_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  level VARCHAR(10) NOT NULL DEFAULT 'INFO',
  message TEXT,
  source VARCHAR(50) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_level (level)
) ENGINE=InnoDB;

INSERT INTO t_logs (level, message, source, created_at)
SELECT
  CASE
    WHEN n % 20 = 0 THEN 'ERROR'
    WHEN n % 10 = 0 THEN 'WARN'
    ELSE 'INFO'
  END AS level,
  CONCAT('Log message #', n) AS message,
  CASE
    WHEN n % 10 = 0 THEN 'legacy_import'
    WHEN n % 5 = 0 THEN 'api_gateway'
    ELSE 'application'
  END AS source,
  DATE_ADD('2023-06-01', INTERVAL (n % 730) DAY) AS created_at
FROM _nums
WHERE n < 5000;

-- ============================================================
-- Table 3: t_events — Composite Index on WHERE
-- 8000 rows, ~1200 system+debug
-- ============================================================
DROP TABLE IF EXISTS t_events;
CREATE TABLE t_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_type VARCHAR(30) NOT NULL,
  severity VARCHAR(10) NOT NULL DEFAULT 'info',
  payload JSON,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_type_severity (event_type, severity),
  KEY idx_created (created_at)
) ENGINE=InnoDB;

INSERT INTO t_events (event_type, severity, payload, created_at)
SELECT
  CASE
    WHEN n % 5 = 0 THEN 'system'
    WHEN n % 3 = 0 THEN 'network'
    ELSE 'application'
  END AS event_type,
  CASE
    WHEN n % 5 = 0 AND n % 3 = 0 THEN 'debug'
    WHEN n % 7 = 0 THEN 'error'
    WHEN n % 4 = 0 THEN 'warn'
    ELSE 'info'
  END AS severity,
  JSON_OBJECT('seq', n, 'ts', NOW()) AS payload,
  DATE_ADD('2023-01-01', INTERVAL (n % 730) DAY) AS created_at
FROM _nums
WHERE n < 8000;

-- ============================================================
-- Table 4: t_sessions — VARCHAR PK (UUID)
-- 5000 rows, ~1000 inactive+expired
-- ============================================================
DROP TABLE IF EXISTS t_sessions;
CREATE TABLE t_sessions (
  session_id VARCHAR(36) NOT NULL,
  user_id INT NOT NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (session_id),
  KEY idx_user (user_id),
  KEY idx_expires (expires_at)
) ENGINE=InnoDB;

INSERT INTO t_sessions (session_id, user_id, is_active, expires_at, created_at)
SELECT
  UUID() AS session_id,
  n AS user_id,
  CASE WHEN n % 5 = 0 THEN 0 ELSE 1 END AS is_active,
  CASE
    WHEN n % 5 = 0 THEN DATE_ADD('2023-06-01', INTERVAL (n % 180) DAY)
    ELSE DATE_ADD('2026-01-01', INTERVAL (n % 365) DAY)
  END AS expires_at,
  DATE_ADD('2023-01-01', INTERVAL (n % 730) DAY) AS created_at
FROM _nums
WHERE n < 5000;

-- ============================================================
-- Table 5: t_ratings — Composite Primary Key (should be rejected)
-- 3000 rows
-- ============================================================
DROP TABLE IF EXISTS t_ratings;
CREATE TABLE t_ratings (
  user_id INT NOT NULL,
  product_id INT NOT NULL,
  score TINYINT NOT NULL,
  comment TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, product_id),
  KEY idx_score (score)
) ENGINE=InnoDB;

INSERT INTO t_ratings (user_id, product_id, score, comment, created_at)
SELECT
  (n DIV 30) AS user_id,
  (n % 30) AS product_id,
  CASE
    WHEN n % 5 = 0 THEN 1
    WHEN n % 5 = 1 THEN 2
    WHEN n % 5 = 2 THEN 3
    WHEN n % 5 = 3 THEN 4
    ELSE 5
  END AS score,
  CONCAT('Rating for product ', n % 30) AS comment,
  DATE_ADD('2024-01-01', INTERVAL (n % 365) DAY) AS created_at
FROM _nums
WHERE n < 3000;

-- ============================================================
-- Table 6: t_order_items — Foreign Key Reference
-- 15000 rows, ~1500 quantity=0
-- t_orders must exist first (created above)
-- ============================================================
DROP TABLE IF EXISTS t_order_items;
CREATE TABLE t_order_items (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id BIGINT UNSIGNED NOT NULL,
  product_name VARCHAR(100) NOT NULL,
  quantity INT NOT NULL DEFAULT 1,
  price DECIMAL(10,2) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_order (order_id),
  CONSTRAINT fk_item_order FOREIGN KEY (order_id) REFERENCES t_orders(id)
) ENGINE=InnoDB;

INSERT INTO t_order_items (order_id, product_name, quantity, price)
SELECT
  (n % 10000) + 1 AS order_id,
  CONCAT('Product-', n % 100) AS product_name,
  CASE WHEN n % 10 = 0 THEN 0 ELSE (n % 5) + 1 END AS quantity,
  ROUND(10 + (n % 990), 2) AS price
FROM _nums
WHERE n < 15000;

-- ============================================================
-- Table 7: t_notifications — DELETE Trigger
-- 6000 rows, ~2400 sent+old
-- ============================================================
DROP TABLE IF EXISTS t_audit_log;
CREATE TABLE t_audit_log (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  table_name VARCHAR(50) NOT NULL,
  action VARCHAR(10) NOT NULL,
  row_id BIGINT NOT NULL,
  old_data JSON,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
) ENGINE=InnoDB;

DROP TABLE IF EXISTS t_notifications;
CREATE TABLE t_notifications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id INT NOT NULL,
  channel VARCHAR(20) NOT NULL DEFAULT 'email',
  status VARCHAR(20) NOT NULL DEFAULT 'pending',
  content TEXT,
  sent_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status_created (status, created_at)
) ENGINE=InnoDB;

INSERT INTO t_notifications (user_id, channel, status, content, sent_at, created_at)
SELECT
  n AS user_id,
  CASE
    WHEN n % 3 = 0 THEN 'sms'
    WHEN n % 3 = 1 THEN 'push'
    ELSE 'email'
  END AS channel,
  CASE
    WHEN n % 5 IN (0,1) THEN 'sent'
    WHEN n % 5 = 2 THEN 'failed'
    ELSE 'pending'
  END AS status,
  CONCAT('Notification #', n) AS content,
  CASE
    WHEN n % 5 IN (0,1) THEN DATE_ADD('2023-06-01', INTERVAL (n % 200) DAY)
    ELSE NULL
  END AS sent_at,
  CASE
    WHEN n % 5 IN (0,1) THEN DATE_ADD('2023-01-01', INTERVAL (n % 300) DAY)
    ELSE DATE_ADD('2025-06-01', INTERVAL (n % 365) DAY)
  END AS created_at
FROM _nums
WHERE n < 6000;

-- Create DELETE trigger on t_notifications
DROP TRIGGER IF EXISTS trg_notifications_del;
DELIMITER //
CREATE TRIGGER trg_notifications_del BEFORE DELETE ON t_notifications
FOR EACH ROW BEGIN
  INSERT INTO t_audit_log (table_name, action, row_id, old_data)
  VALUES ('t_notifications', 'DELETE', OLD.id,
    JSON_OBJECT('user_id', OLD.user_id, 'status', OLD.status, 'channel', OLD.channel));
END //
DELIMITER ;

-- ============================================================
-- Table 8: t_users — Unique Index (UK as chunk column candidate)
-- INT PK + unique index on email. Tests whether unique index is preferred.
-- ============================================================
DROP TABLE IF EXISTS t_users;
CREATE TABLE t_users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(255) NOT NULL,
  phone VARCHAR(20) DEFAULT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'active',
  region VARCHAR(10) NOT NULL DEFAULT 'cn',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_email (email),
  KEY idx_status (status),
  KEY idx_region (region)
) ENGINE=InnoDB;

INSERT INTO t_users (email, phone, status, region, created_at)
SELECT
  CONCAT('user', n, '@example.com') AS email,
  CONCAT('138', LPAD(n, 8, '0')) AS phone,
  CASE
    WHEN n % 7 = 0 THEN 'inactive'
    WHEN n % 5 = 0 THEN 'suspended'
    ELSE 'active'
  END AS status,
  CASE
    WHEN n % 4 = 0 THEN 'us'
    WHEN n % 4 = 1 THEN 'eu'
    ELSE 'cn'
  END AS region,
  CASE
    WHEN n % 7 = 0 THEN DATE_ADD('2023-01-01', INTERVAL (n % 365) DAY)
    ELSE DATE_ADD('2025-01-01', INTERVAL (n % 365) DAY)
  END AS created_at
FROM _nums
WHERE n < 8000;

-- ============================================================
-- Table 9: t_metrics — Sparse Data
-- 20000 rows, ~400 error+old
-- ============================================================
DROP TABLE IF EXISTS t_metrics;
CREATE TABLE t_metrics (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  host VARCHAR(50) NOT NULL,
  metric_name VARCHAR(50) NOT NULL,
  value DOUBLE NOT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'ok',
  recorded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status (status),
  KEY idx_recorded (recorded_at)
) ENGINE=InnoDB;

INSERT INTO t_metrics (host, metric_name, value, status, recorded_at)
SELECT
  CONCAT('host-', n % 50) AS host,
  CASE
    WHEN n % 4 = 0 THEN 'cpu_usage'
    WHEN n % 4 = 1 THEN 'memory_usage'
    WHEN n % 4 = 2 THEN 'disk_io'
    ELSE 'network_io'
  END AS metric_name,
  ROUND(RAND() * 100, 2) AS value,
  CASE
    WHEN n % 50 = 0 THEN 'error'
    WHEN n % 10 = 0 THEN 'warning'
    ELSE 'ok'
  END AS status,
  CASE
    WHEN n % 50 = 0 THEN DATE_ADD('2023-01-01', INTERVAL (n % 365) DAY)
    ELSE DATE_ADD('2025-01-01', INTERVAL (n % 540) DAY)
  END AS recorded_at
FROM _nums
WHERE n < 20000;

-- ============================================================
-- Table 10: t_big_unsigned — BIGINT UNSIGNED near max value
-- PK values near 2^64 to test big.Int handling
-- 500 rows inserted at high PK range
-- ============================================================
DROP TABLE IF EXISTS t_big_unsigned;
CREATE TABLE t_big_unsigned (
  id BIGINT UNSIGNED NOT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status (status)
) ENGINE=InnoDB;

INSERT INTO t_big_unsigned (id, status, created_at)
SELECT
  18446744073709551615 - n AS id,
  CASE
    WHEN n % 5 IN (0,1) THEN 'expired'
    ELSE 'active'
  END AS status,
  CASE
    WHEN n % 5 IN (0,1) THEN DATE_ADD('2023-01-01', INTERVAL (n % 365) DAY)
    ELSE DATE_ADD('2025-01-01', INTERVAL (n % 365) DAY)
  END AS created_at
FROM _nums
WHERE n BETWEEN 1 AND 499;

-- Insert explicit rows above int64 max (9223372036854775807)
INSERT INTO t_big_unsigned (id, status, created_at) VALUES
  (9223372036854775808, 'expired', '2023-06-01 00:00:00'),
  (9223372036854775809, 'expired', '2023-07-01 00:00:00'),
  (10000000000000000000, 'expired', '2023-08-01 00:00:00'),
  (18000000000000000000, 'active',  '2025-01-01 00:00:00'),
  (18446744073709551615, 'expired', '2023-09-01 00:00:00');

-- ============================================================
-- Table 11: t_negative_pk — Negative PK values
-- Tests handling of signed BIGINT with negative values
-- 500 rows spanning negative to positive range
-- ============================================================
DROP TABLE IF EXISTS t_negative_pk;
CREATE TABLE t_negative_pk (
  id BIGINT NOT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status (status)
) ENGINE=InnoDB;

INSERT INTO t_negative_pk (id, status, created_at)
SELECT
  n - 250 AS id,
  CASE
    WHEN n % 5 IN (0,1) THEN 'expired'
    ELSE 'active'
  END AS status,
  CASE
    WHEN n % 5 IN (0,1) THEN DATE_ADD('2023-01-01', INTERVAL (n % 365) DAY)
    ELSE DATE_ADD('2025-01-01', INTERVAL (n % 365) DAY)
  END AS created_at
FROM _nums
WHERE n < 500;

-- ============================================================
-- Table 12: t_huge_range — Sparse rows across huge PK range
-- PK range spans 0 to 10 billion, only ~200 rows inserted
-- Tests efficient sparse chunk handling with big.Int
-- ============================================================
DROP TABLE IF EXISTS t_huge_range;
CREATE TABLE t_huge_range (
  id BIGINT UNSIGNED NOT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'active',
  value DOUBLE NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status (status)
) ENGINE=InnoDB;

INSERT INTO t_huge_range (id, status, value, created_at)
SELECT
  n * 50000000 AS id,
  CASE
    WHEN n % 5 IN (0,1) THEN 'expired'
    ELSE 'active'
  END AS status,
  ROUND(RAND() * 100, 2) AS value,
  CASE
    WHEN n % 5 IN (0,1) THEN DATE_ADD('2023-01-01', INTERVAL (n % 365) DAY)
    ELSE DATE_ADD('2025-01-01', INTERVAL (n % 365) DAY)
  END AS created_at
FROM _nums
WHERE n < 200;

-- ============================================================
-- Summary
-- ============================================================
SELECT 't_orders' AS tbl, COUNT(*) AS total, SUM(status='expired' AND created_at < '2024-01-01') AS matching FROM t_orders
UNION ALL
SELECT 't_logs', COUNT(*), SUM(source='legacy_import') FROM t_logs
UNION ALL
SELECT 't_events', COUNT(*), SUM(event_type='system' AND severity='debug') FROM t_events
UNION ALL
SELECT 't_sessions', COUNT(*), SUM(is_active=0 AND expires_at < '2024-01-01') FROM t_sessions
UNION ALL
SELECT 't_ratings', COUNT(*), SUM(score=1) FROM t_ratings
UNION ALL
SELECT 't_order_items', COUNT(*), SUM(quantity=0) FROM t_order_items
UNION ALL
SELECT 't_notifications', COUNT(*), SUM(status='sent' AND created_at < '2024-01-01') FROM t_notifications
UNION ALL
SELECT 't_users', COUNT(*), SUM(status='inactive' AND created_at < '2024-01-01') FROM t_users
UNION ALL
SELECT 't_metrics', COUNT(*), SUM(status='error' AND recorded_at < '2024-01-01') FROM t_metrics;

-- Cleanup
DROP PROCEDURE IF EXISTS _generate_numbers;
SET FOREIGN_KEY_CHECKS=1;
