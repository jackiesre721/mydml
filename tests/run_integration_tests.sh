#!/usr/bin/env bash
# ============================================================
# Integration Test Suite for mydml
# Usage: ./tests/run_integration_tests.sh [MYSQL_OPTS]
# Example: ./tests/run_integration_tests.sh --host=127.0.0.1 --user=root --database=delete_test
# ============================================================
set -euo pipefail

# Defaults (env vars take precedence)
MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-}"
MYSQL_DATABASE="${MYSQL_DATABASE:-delete_test}"
BATCH_SIZE=500
SLEEP_MS=50

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --host=*)       MYSQL_HOST="${1#*=}"; shift ;;
    --port=*)       MYSQL_PORT="${1#*=}"; shift ;;
    --user=*)       MYSQL_USER="${1#*=}"; shift ;;
    --password=*)   MYSQL_PASSWORD="${1#*=}"; shift ;;
    --database=*)   MYSQL_DATABASE="${1#*=}"; shift ;;
    *)              shift ;;
  esac
done

BINARY="./mydml"
MYSQL_CMD="mysql -u $MYSQL_USER"
[[ -n "$MYSQL_PASSWORD" ]] && MYSQL_CMD="$MYSQL_CMD -p$MYSQL_PASSWORD"
MYSQL_CMD="$MYSQL_CMD -h $MYSQL_HOST -P $MYSQL_PORT $MYSQL_DATABASE"

PASS=0
FAIL=0
SKIP=0
RESULTS=()

log() { echo "[$(date '+%H:%M:%S')] $*"; }
pass() { PASS=$((PASS+1)); RESULTS+=("PASS  $1"); log "\033[32mPASS\033[0m $1"; }
fail() { FAIL=$((FAIL+1)); RESULTS+=("FAIL  $1"); log "\033[31mFAIL\033[0m $1"; }

# Run tool, always succeeds (captures output). Logs go to logfile.
run_tool() {
  local logfile="$1"; shift
  "$BINARY" delete \
    --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
    --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
    --sleep-ms="$SLEEP_MS" --batch-size="$BATCH_SIZE" \
    --log-file="$logfile" \
    $([[ -n "$MYSQL_PASSWORD" ]] && echo "--password=$MYSQL_PASSWORD") \
    "$@" 2>&1 || true
}

# Run update subcommand
run_update() {
  local logfile="$1"; shift
  "$BINARY" update \
    --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
    --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
    --sleep-ms="$SLEEP_MS" --batch-size="$BATCH_SIZE" \
    --log-file="$logfile" \
    $([[ -n "$MYSQL_PASSWORD" ]] && echo "--password=$MYSQL_PASSWORD") \
    "$@" 2>&1 || true
}

# Run insert-select subcommand
run_insert_select() {
  local logfile="$1"; shift
  "$BINARY" insert-select \
    --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
    --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
    --sleep-ms="$SLEEP_MS" --batch-size="$BATCH_SIZE" \
    --log-file="$logfile" \
    $([[ -n "$MYSQL_PASSWORD" ]] && echo "--password=$MYSQL_PASSWORD") \
    "$@" 2>&1 || true
}

# Run tool expecting failure, return output for pattern matching
run_tool_fail() {
  local logfile="$1"; shift
  run_tool "$logfile" "$@"
}

log_has() {
  grep -q "$2" "$1"
}

count_matching() {
  local table="$1" where="$2"
  $MYSQL_CMD -N -e "SELECT COUNT(*) FROM $table WHERE $where"
}

count_total() {
  local table="$1"
  $MYSQL_CMD -N -e "SELECT COUNT(*) FROM $table"
}

# Setup: create all tables, drop FK child so t_orders can be tested freely
setup_no_fk() {
  $MYSQL_CMD < tests/create_test_tables.sql
  $MYSQL_CMD -e "SET FOREIGN_KEY_CHECKS=0; DROP TABLE IF EXISTS t_order_items; SET FOREIGN_KEY_CHECKS=1;"
}

setup_with_fk() {
  $MYSQL_CMD < tests/create_test_tables.sql
}

# ============================================================
# Setup
# ============================================================
log "Building binary..."
go build -o "$BINARY" ./cmd/mydml

log "Creating test tables..."
setup_no_fk

LOGDIR=$(mktemp -d /tmp/mysql-delete-test-XXXXXX)
log "Log directory: $LOGDIR"

# ============================================================
# Test 1: Happy path — t_orders (INDEXED/RANGE)
# ============================================================
log "--- Test 1: Happy path delete (t_orders) ---"
run_tool "$LOGDIR/test1.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'"
if log_has "$LOGDIR/test1.log" "plan generated" \
  && log_has "$LOGDIR/test1.log" "task completed" \
  && log_has "$LOGDIR/test1.log" "ORDER BY" \
  && [[ "$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")" -eq 0 ]]; then
  pass "Test 1: Happy path"
else
  fail "Test 1: Happy path"
fi

# ============================================================
# Test 2: Dry-run mode
# ============================================================
log "--- Test 2: Dry-run (t_orders) ---"
setup_no_fk
run_tool "$LOGDIR/test2.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --dry-run
if log_has "$LOGDIR/test2.log" "dry_run=true" \
  && log_has "$LOGDIR/test2.log" "task completed" \
  && [[ "$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")" -eq 3000 ]]; then
  pass "Test 2: Dry-run"
else
  fail "Test 2: Dry-run"
fi

# ============================================================
# Test 3: Max-rows limit
# ============================================================
log "--- Test 3: Max-rows limit (t_orders) ---"
setup_no_fk
run_tool "$LOGDIR/test3.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --max-rows=500
remaining=$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")
# max-rows=500 but check is per-chunk (~150 rows/chunk), so actual deleted is a multiple
if log_has "$LOGDIR/test3.log" "max rows limit reached" \
  && [[ "$remaining" -ge 2400 ]] && [[ "$remaining" -lt 3000 ]]; then
  pass "Test 3: Max-rows limit"
else
  fail "Test 3: Max-rows limit (remaining=$remaining)"
fi

# ============================================================
# Test 4: No index on WHERE — full scan fallback
# ============================================================
log "--- Test 4: No index fallback (t_logs) ---"
setup_no_fk
run_tool "$LOGDIR/test4.log" \
  --table=t_logs \
  --where="source = 'legacy_import'"
if log_has "$LOGDIR/test4.log" "plan generated" \
  && log_has "$LOGDIR/test4.log" "task completed" \
  && [[ "$(count_matching t_logs "source = 'legacy_import'")" -eq 0 ]]; then
  pass "Test 4: No index fallback"
else
  fail "Test 4: No index fallback"
fi

# ============================================================
# Test 5: Composite index on WHERE
# ============================================================
log "--- Test 5: Composite index (t_events) ---"
run_tool "$LOGDIR/test5.log" \
  --table=t_events \
  --where="event_type = 'system' AND severity = 'debug'"
if log_has "$LOGDIR/test5.log" "plan generated" \
  && log_has "$LOGDIR/test5.log" "task completed" \
  && [[ "$(count_matching t_events "event_type = 'system' AND severity = 'debug'")" -eq 0 ]]; then
  pass "Test 5: Composite index"
else
  fail "Test 5: Composite index"
fi

# ============================================================
# Test 6: VARCHAR PK — no unique secondary index, should be rejected
# ============================================================
log "--- Test 6: VARCHAR PK (t_sessions) ---"
run_tool "$LOGDIR/test6.log" \
  --table=t_sessions \
  --where="is_active = 0 AND expires_at < '2024-01-01'"
if log_has "$LOGDIR/test6.log" "non-numeric"; then
  pass "Test 6: VARCHAR PK"
else
  fail "Test 6: VARCHAR PK"
fi

# ============================================================
# Test 7: Composite PK — should be rejected
# ============================================================
log "--- Test 7: Composite PK rejection (t_ratings) ---"
output=$(run_tool_fail "$LOGDIR/test7.log" \
  --table=t_ratings --where="score = 1")
if echo "$output" | grep -qi "composite primary key"; then
  pass "Test 7: Composite PK rejection"
else
  fail "Test 7: Composite PK rejection"
fi

# ============================================================
# Test 8: FK rejection — t_orders referenced by t_order_items
# ============================================================
log "--- Test 8: FK rejection (t_orders with FK child) ---"
setup_with_fk
run_tool_fail "$LOGDIR/test8.log" \
  --table=t_orders --where="status = 'expired' AND created_at < '2024-01-01'"
if log_has "$LOGDIR/test8.log" "foreign key"; then
  pass "Test 8: FK rejection"
else
  fail "Test 8: FK rejection"
fi

# ============================================================
# Test 9: DELETE trigger — should warn but proceed
# ============================================================
log "--- Test 9: Trigger warning (t_notifications) ---"
setup_no_fk
run_tool "$LOGDIR/test9.log" \
  --table=t_notifications \
  --where="status = 'sent' AND created_at < '2024-01-01'"
if log_has "$LOGDIR/test9.log" "DELETE trigger" \
  && log_has "$LOGDIR/test9.log" "task completed" \
  && [[ "$(count_matching t_notifications "status = 'sent' AND created_at < '2024-01-01'")" -eq 0 ]]; then
  pass "Test 9: Trigger warning"
else
  fail "Test 9: Trigger warning"
fi

# ============================================================
# Test 10: Sparse data — most chunks empty
# ============================================================
log "--- Test 10: Sparse data (t_metrics) ---"
run_tool "$LOGDIR/test10.log" \
  --table=t_metrics \
  --where="status = 'error' AND recorded_at < '2024-01-01'"
if log_has "$LOGDIR/test10.log" "plan generated" \
  && log_has "$LOGDIR/test10.log" "task completed" \
  && [[ "$(count_matching t_metrics "status = 'error' AND recorded_at < '2024-01-01'")" -eq 0 ]]; then
  pass "Test 10: Sparse data"
else
  fail "Test 10: Sparse data"
fi

# ============================================================
# Test 11: Unique index (t_users)
# ============================================================
log "--- Test 11: Unique index table (t_users) ---"
run_tool "$LOGDIR/test11.log" \
  --table=t_users \
  --where="status = 'inactive' AND created_at < '2024-01-01'"
if log_has "$LOGDIR/test11.log" "plan generated" \
  && log_has "$LOGDIR/test11.log" "task completed" \
  && [[ "$(count_matching t_users "status = 'inactive' AND created_at < '2024-01-01'")" -eq 0 ]]; then
  pass "Test 11: Unique index"
else
  fail "Test 11: Unique index"
fi

# ============================================================
# Test 12: Small batch size
# ============================================================
log "--- Test 12: Small batch size (t_orders) ---"
setup_no_fk
run_tool "$LOGDIR/test12.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --batch-size=100
if log_has "$LOGDIR/test12.log" "task completed" \
  && log_has "$LOGDIR/test12.log" "batch_size" \
  && [[ "$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")" -eq 0 ]]; then
  pass "Test 12: Small batch"
else
  fail "Test 12: Small batch"
fi

# ============================================================
# GROUP A: Input Validation & Error Handling
# ============================================================

# Test 13: Missing --table flag
log "--- Test 13: Missing --table ---"
output=$("$BINARY" delete \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --where="1=1" 2>&1 || true)
if echo "$output" | grep -qi "required"; then
  pass "Test 13: Missing --table"
else
  fail "Test 13: Missing --table"
fi

# Test 14: Missing --where flag
log "--- Test 14: Missing --where ---"
output=$("$BINARY" delete \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --table=t_orders 2>&1 || true)
if echo "$output" | grep -qi "required"; then
  pass "Test 14: Missing --where"
else
  fail "Test 14: Missing --where"
fi

# Test 15: Invalid batch_size (too small)
log "--- Test 15: Invalid batch_size (50) ---"
output=$("$BINARY" delete \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --table=t_orders --where="1=1" --batch-size=50 2>&1 || true)
if echo "$output" | grep -qi "batch_size\|validation"; then
  pass "Test 15: Invalid batch_size"
else
  fail "Test 15: Invalid batch_size"
fi

# Test 16: Invalid batch_size (too large)
log "--- Test 16: Invalid batch_size (10000) ---"
output=$("$BINARY" delete \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --table=t_orders --where="1=1" --batch-size=10000 2>&1 || true)
if echo "$output" | grep -qi "batch_size\|validation"; then
  pass "Test 16: Oversized batch_size"
else
  fail "Test 16: Oversized batch_size"
fi

# Test 17: Non-existent table
log "--- Test 17: Non-existent table ---"
output=$(run_tool_fail "$LOGDIR/test17.log" \
  --table=nonexistent_table_xyz --where="1=1")
if echo "$output" | grep -qi "error\|not exist\|failed"; then
  pass "Test 17: Non-existent table"
else
  fail "Test 17: Non-existent table"
fi

# ============================================================
# GROUP B: Boundary Conditions
# ============================================================

# Test 18: WHERE matches 0 rows (empty result)
log "--- Test 18: Zero rows match ---"
setup_no_fk
run_tool "$LOGDIR/test18.log" \
  --table=t_orders \
  --where="status = 'nonexistent_status_xyz'"
if log_has "$LOGDIR/test18.log" "task completed"; then
  pass "Test 18: Zero rows match"
else
  fail "Test 18: Zero rows match"
fi

# Test 19: Single row match
log "--- Test 19: Single row match ---"
setup_no_fk
# Insert one specific row to delete
$MYSQL_CMD -e "INSERT INTO t_orders (user_id, status, amount, created_at) VALUES (99999, 'archived', 1.00, '2020-01-01')"
run_tool "$LOGDIR/test19.log" \
  --table=t_orders \
  --where="status = 'archived'"
if log_has "$LOGDIR/test19.log" "task completed" \
  && [[ "$(count_matching t_orders "status = 'archived'")" -eq 0 ]]; then
  pass "Test 19: Single row match"
else
  fail "Test 19: Single row match"
fi

# Test 20: Non-contiguous PK (gaps from prior deletes)
log "--- Test 20: Non-contiguous PK (gaps) ---"
setup_no_fk
# Delete every other row to create gaps
$MYSQL_CMD -e "DELETE FROM t_orders WHERE id % 2 = 0"
# Now delete remaining expired rows across gaps
before=$(count_total t_orders)
run_tool "$LOGDIR/test20.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'"
if log_has "$LOGDIR/test20.log" "task completed" \
  && [[ "$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")" -eq 0 ]]; then
  pass "Test 20: Non-contiguous PK"
else
  fail "Test 20: Non-contiguous PK"
fi

# Test 21: All rows match (dense table)
log "--- Test 21: All rows match (dense) ---"
setup_no_fk
# Make all rows match the condition by updating all to expired
$MYSQL_CMD -e "UPDATE t_orders SET status = 'expired', created_at = '2023-01-01' WHERE status != 'expired' OR created_at >= '2024-01-01'"
total=$(count_total t_orders)
run_tool "$LOGDIR/test21.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'"
if log_has "$LOGDIR/test21.log" "task completed" \
  && [[ "$(count_total t_orders)" -eq 0 ]]; then
  pass "Test 21: All rows match"
else
  fail "Test 21: All rows match"
fi

# ============================================================
# GROUP C: WHERE Clause Variations
# ============================================================

# Test 22: WHERE with OR
log "--- Test 22: WHERE with OR ---"
setup_no_fk
run_tool "$LOGDIR/test22.log" \
  --table=t_orders \
  --where="status = 'expired' OR status = 'cancelled'"
if log_has "$LOGDIR/test22.log" "task completed" \
  && [[ "$(count_matching t_orders "status = 'expired' OR status = 'cancelled'")" -eq 0 ]]; then
  pass "Test 22: WHERE with OR"
else
  fail "Test 22: WHERE with OR"
fi

# Test 23: WHERE with IN
log "--- Test 23: WHERE with IN ---"
setup_no_fk
run_tool "$LOGDIR/test23.log" \
  --table=t_orders \
  --where="status IN ('expired', 'cancelled')"
if log_has "$LOGDIR/test23.log" "task completed" \
  && [[ "$(count_matching t_orders "status IN ('expired', 'cancelled')")" -eq 0 ]]; then
  pass "Test 23: WHERE with IN"
else
  fail "Test 23: WHERE with IN"
fi

# Test 24: WHERE with BETWEEN
log "--- Test 24: WHERE with BETWEEN ---"
setup_no_fk
run_tool "$LOGDIR/test24.log" \
  --table=t_orders \
  --where="created_at BETWEEN '2023-01-01' AND '2023-12-31'"
if log_has "$LOGDIR/test24.log" "task completed" \
  && [[ "$(count_matching t_orders "created_at BETWEEN '2023-01-01' AND '2023-12-31'")" -eq 0 ]]; then
  pass "Test 24: WHERE with BETWEEN"
else
  fail "Test 24: WHERE with BETWEEN"
fi

# Test 25: WHERE with IS NULL
log "--- Test 25: WHERE with IS NULL ---"
setup_no_fk
# Add some rows with NULL-like condition (use a column that can be null)
$MYSQL_CMD -e "ALTER TABLE t_logs ADD COLUMN deleted_at DATETIME NULL DEFAULT NULL AFTER created_at"
$MYSQL_CMD -e "UPDATE t_logs SET deleted_at = '2023-06-01' WHERE source = 'legacy_import'"
run_tool "$LOGDIR/test25.log" \
  --table=t_logs \
  --where="deleted_at IS NOT NULL"
if log_has "$LOGDIR/test25.log" "task completed" \
  && [[ "$(count_matching t_logs "deleted_at IS NOT NULL")" -eq 0 ]]; then
  pass "Test 25: WHERE with IS NOT NULL"
else
  fail "Test 25: WHERE with IS NOT NULL"
fi

# ============================================================
# GROUP D: Operational & Throttle
# ============================================================

# Test 26: Idempotent re-run (second run finds 0 rows)
log "--- Test 26: Idempotent re-run ---"
setup_no_fk
# First run: delete all expired
run_tool "$LOGDIR/test26a.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'"
# Second run: same condition, should find 0 rows
run_tool "$LOGDIR/test26b.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'"
if log_has "$LOGDIR/test26b.log" "task completed"; then
  pass "Test 26: Idempotent re-run"
else
  fail "Test 26: Idempotent re-run"
fi

# Test 27: Custom task-id
log "--- Test 27: Custom task-id ---"
setup_no_fk
run_tool "$LOGDIR/test27.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --task-id="my-custom-task-001"
if log_has "$LOGDIR/test27.log" "task_id=my-custom-task-001" \
  && log_has "$LOGDIR/test27.log" "task completed"; then
  pass "Test 27: Custom task-id"
else
  fail "Test 27: Custom task-id"
fi

# Test 28: Nice-ratio throttle
log "--- Test 28: Nice-ratio throttle ---"
setup_no_fk
run_tool "$LOGDIR/test28.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --nice-ratio=1.0
if log_has "$LOGDIR/test28.log" "task completed" \
  && log_has "$LOGDIR/test28.log" "nice_ratio" ; then
  pass "Test 28: Nice-ratio throttle"
else
  fail "Test 28: Nice-ratio throttle"
fi

# Test 29: HTTP control — pause/resume
log "--- Test 29: HTTP pause/resume ---"
setup_no_fk
# Start tool in background
run_tool "$LOGDIR/test29.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" &
TOOL_PID=$!
# Wait for task to start, then pause
sleep 0.5
curl -s -X POST http://127.0.0.1:8080/api/v1/pause 2>/dev/null || true
sleep 0.3
curl -s -X POST http://127.0.0.1:8080/api/v1/resume 2>/dev/null || true
# Wait for completion
wait $TOOL_PID 2>/dev/null || true
if log_has "$LOGDIR/test29.log" "task completed" \
  && [[ "$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")" -eq 0 ]]; then
  pass "Test 29: HTTP pause/resume"
else
  fail "Test 29: HTTP pause/resume"
fi

# Test 30: HTTP control — stop mid-task
log "--- Test 30: HTTP stop mid-task ---"
setup_no_fk
# Use small batch to ensure task runs long enough to stop
run_tool "$LOGDIR/test30.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --batch-size=100 --sleep-ms=200 &
TOOL_PID=$!
sleep 0.8
curl -s -X POST http://127.0.0.1:8080/api/v1/stop 2>/dev/null || true
wait $TOOL_PID 2>/dev/null || true
# Should have deleted some but not all
remaining=$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")
if log_has "$LOGDIR/test30.log" "stop signal\|stop" \
  && [[ "$remaining" -gt 0 ]] && [[ "$remaining" -lt 3000 ]]; then
  pass "Test 30: HTTP stop mid-task (remaining=$remaining)"
else
  fail "Test 30: HTTP stop mid-task (remaining=$remaining)"
fi

# ============================================================
# GROUP E: Data Correctness
# ============================================================

# Test 31: Non-matching rows are untouched
log "--- Test 31: Non-matching rows untouched ---"
setup_no_fk
# Capture active orders before
active_before=$($MYSQL_CMD -N -e "SELECT COUNT(*) FROM t_orders WHERE status = 'active'")
run_tool "$LOGDIR/test31.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'"
active_after=$($MYSQL_CMD -N -e "SELECT COUNT(*) FROM t_orders WHERE status = 'active'")
if log_has "$LOGDIR/test31.log" "task completed" \
  && [[ "$active_before" -eq "$active_after" ]]; then
  pass "Test 31: Non-matching rows untouched (before=$active_before after=$active_after)"
else
  fail "Test 31: Non-matching rows untouched (before=$active_before after=$active_after)"
fi

# Test 32: Trigger audit table populated correctly
log "--- Test 32: Trigger audit trail ---"
setup_no_fk
audit_before=$($MYSQL_CMD -N -e "SELECT COUNT(*) FROM t_audit_log")
run_tool "$LOGDIR/test32.log" \
  --table=t_notifications \
  --where="status = 'sent' AND created_at < '2024-01-01'"
audit_after=$($MYSQL_CMD -N -e "SELECT COUNT(*) FROM t_audit_log")
if log_has "$LOGDIR/test32.log" "task completed" \
  && [[ "$audit_after" -gt "$audit_before" ]]; then
  pass "Test 32: Trigger audit trail (audit rows: $audit_before -> $audit_after)"
else
  fail "Test 32: Trigger audit trail (audit rows: $audit_before -> $audit_after)"
fi

# Test 33: SQL annotation in every query
log "--- Test 33: SQL annotation present ---"
setup_no_fk
run_tool "$LOGDIR/test33.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --task-id="annotation-test-33"
if log_has "$LOGDIR/test33.log" "mydml:task=annotation-test-33" \
  && log_has "$LOGDIR/test33.log" "ORDER BY" \
  && log_has "$LOGDIR/test33.log" "DELETE FROM"; then
  pass "Test 33: SQL annotation"
else
  fail "Test 33: SQL annotation"
fi

# ============================================================
# GROUP F: UPDATE Mode
# ============================================================

# Test 34: UPDATE happy path — change status column
log "--- Test 34: UPDATE happy path (t_orders) ---"
setup_no_fk
run_update "$LOGDIR/test34.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --set="status = 'archived'"
if log_has "$LOGDIR/test34.log" "task completed" \
  && log_has "$LOGDIR/test34.log" "mode=update" \
  && [[ "$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")" -eq 0 ]] \
  && [[ "$(count_matching t_orders "status = 'archived'")" -eq 3000 ]]; then
  pass "Test 34: UPDATE happy path"
else
  fail "Test 34: UPDATE happy path"
fi

# Test 35: UPDATE dry-run mode
log "--- Test 35: UPDATE dry-run (t_orders) ---"
setup_no_fk
run_update "$LOGDIR/test35.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --set="status = 'archived'" \
  --dry-run
if log_has "$LOGDIR/test35.log" "dry_run=true" \
  && log_has "$LOGDIR/test35.log" "task completed" \
  && [[ "$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")" -eq 3000 ]]; then
  pass "Test 35: UPDATE dry-run"
else
  fail "Test 35: UPDATE dry-run"
fi

# Test 36: UPDATE with max-rows
log "--- Test 36: UPDATE max-rows (t_orders) ---"
setup_no_fk
run_update "$LOGDIR/test36.log" \
  --table=t_orders \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --set="status = 'pending'" \
  --max-rows=500
remaining=$(count_matching t_orders "status = 'expired' AND created_at < '2024-01-01'")
if log_has "$LOGDIR/test36.log" "max rows limit reached" \
  && [[ "$remaining" -ge 2400 ]] && [[ "$remaining" -lt 3000 ]]; then
  pass "Test 36: UPDATE max-rows"
else
  fail "Test 36: UPDATE max-rows (remaining=$remaining)"
fi


# Test 37: UPDATE missing --set flag
log "--- Test 37: UPDATE missing --set ---"
output=$("$BINARY" update \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --table=t_orders --where="1=1" 2>&1 || true)
if echo "$output" | grep -qi "set.*required\|required.*set"; then
  pass "Test 37: UPDATE missing --set"
else
  fail "Test 37: UPDATE missing --set"
fi

# ============================================================
# GROUP G: INSERT_SELECT Mode
# ============================================================

# Test 38: INSERT_SELECT happy path
log "--- Test 38: INSERT_SELECT happy path ---"
setup_no_fk
# Create archive table with same structure
$MYSQL_CMD -e "DROP TABLE IF EXISTS t_orders_archive; CREATE TABLE t_orders_archive LIKE t_orders;"
run_insert_select "$LOGDIR/test38.log" \
  --source-table=t_orders \
  --target-table=t_orders_archive \
  --where="status = 'expired' AND created_at < '2024-01-01'"
archive_count=$(count_total t_orders_archive)
if log_has "$LOGDIR/test38.log" "task completed" \
  && log_has "$LOGDIR/test38.log" "mode=insert_select" \
  && [[ "$archive_count" -eq 3000 ]]; then
  pass "Test 38: INSERT_SELECT happy path (archived=$archive_count)"
else
  fail "Test 38: INSERT_SELECT happy path (archived=$archive_count)"
fi

# Test 39: INSERT_SELECT dry-run
log "--- Test 39: INSERT_SELECT dry-run ---"
setup_no_fk
$MYSQL_CMD -e "DROP TABLE IF EXISTS t_orders_archive; CREATE TABLE t_orders_archive LIKE t_orders;"
run_insert_select "$LOGDIR/test39.log" \
  --source-table=t_orders \
  --target-table=t_orders_archive \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --dry-run
archive_count=$(count_total t_orders_archive)
if log_has "$LOGDIR/test39.log" "dry_run=true" \
  && log_has "$LOGDIR/test39.log" "task completed" \
  && [[ "$archive_count" -eq 0 ]]; then
  pass "Test 39: INSERT_SELECT dry-run"
else
  fail "Test 39: INSERT_SELECT dry-run (archived=$archive_count)"
fi

# Test 40: INSERT_SELECT missing --target-table
log "--- Test 40: INSERT_SELECT missing --target-table ---"
output=$("$BINARY" insert-select \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --source-table=t_orders --where="1=1" 2>&1 || true)
if echo "$output" | grep -qi "target-table.*required\|required.*target-table"; then
  pass "Test 40: INSERT_SELECT missing --target-table"
else
  fail "Test 40: INSERT_SELECT missing --target-table"
fi

# Test 41: INSERT_SELECT same source and target table
log "--- Test 41: INSERT_SELECT same table ---"
output=$("$BINARY" insert-select \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --source-table=t_orders --target-table=t_orders --where="1=1" 2>&1 || true)
if echo "$output" | grep -qi "must be different\|same"; then
  pass "Test 41: INSERT_SELECT same table"
else
  fail "Test 41: INSERT_SELECT same table"
fi

# ============================================================
# GROUP H: WHERE Constraint Validation
# ============================================================

# Test 42: WHERE with subquery rejected
log "--- Test 42: WHERE with subquery rejected ---"
output=$("$BINARY" delete \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --table=t_orders --where="id IN (SELECT id FROM t_orders WHERE status = 'expired')" 2>&1 || true)
if echo "$output" | grep -qi "subquer"; then
  pass "Test 42: WHERE subquery rejected"
else
  fail "Test 42: WHERE subquery rejected"
fi

# Test 43: WHERE with LIMIT rejected
log "--- Test 43: WHERE with LIMIT rejected ---"
output=$("$BINARY" delete \
  --host="$MYSQL_HOST" --port="$MYSQL_PORT" \
  --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --table=t_orders --where="status = 'expired' LIMIT 100" 2>&1 || true)
if echo "$output" | grep -qi "LIMIT\|limit"; then
  pass "Test 43: WHERE LIMIT rejected"
else
  fail "Test 43: WHERE LIMIT rejected"
fi

# ============================================================
# GROUP I: Extreme Value Tests (BIGINT UNSIGNED, Negative PK, Huge Range)
# ============================================================

# Test 44: BIGINT UNSIGNED near max value
log "--- Test 44: BIGINT UNSIGNED near max ---"
setup_no_fk
before=$(count_matching t_big_unsigned "status = 'expired' AND created_at < '2024-01-01'")
run_tool "$LOGDIR/test44.log" \
  --table=t_big_unsigned \
  --where="status = 'expired' AND created_at < '2024-01-01'"
after=$(count_matching t_big_unsigned "status = 'expired' AND created_at < '2024-01-01'")
if log_has "$LOGDIR/test44.log" "plan generated" \
  && log_has "$LOGDIR/test44.log" "task completed" \
  && [[ "$before" -gt 0 ]] \
  && [[ "$after" -eq 0 ]]; then
  pass "Test 44: BIGINT UNSIGNED near max (deleted $before)"
else
  fail "Test 44: BIGINT UNSIGNED near max (before=$before after=$after)"
fi

# Test 45: Negative PK values
log "--- Test 45: Negative PK ---"
before=$(count_matching t_negative_pk "status = 'expired' AND created_at < '2024-01-01'")
run_tool "$LOGDIR/test45.log" \
  --table=t_negative_pk \
  --where="status = 'expired' AND created_at < '2024-01-01'"
after=$(count_matching t_negative_pk "status = 'expired' AND created_at < '2024-01-01'")
if log_has "$LOGDIR/test45.log" "plan generated" \
  && log_has "$LOGDIR/test45.log" "task completed" \
  && [[ "$before" -gt 0 ]] \
  && [[ "$after" -eq 0 ]]; then
  pass "Test 45: Negative PK (deleted $before)"
else
  fail "Test 45: Negative PK (before=$before after=$after)"
fi

# Test 46: Huge PK range with sparse data
log "--- Test 46: Huge PK range sparse ---"
before=$(count_matching t_huge_range "status = 'expired' AND created_at < '2024-01-01'")
run_tool "$LOGDIR/test46.log" \
  --table=t_huge_range \
  --where="status = 'expired' AND created_at < '2024-01-01'" \
  --batch-size=1000000
after=$(count_matching t_huge_range "status = 'expired' AND created_at < '2024-01-01'")
if log_has "$LOGDIR/test46.log" "plan generated" \
  && log_has "$LOGDIR/test46.log" "task completed" \
  && [[ "$before" -gt 0 ]] \
  && [[ "$after" -eq 0 ]]; then
  pass "Test 46: Huge PK range sparse (deleted $before)"
else
  fail "Test 46: Huge PK range sparse (before=$before after=$after)"
fi

# ============================================================
# Summary
# ============================================================
echo ""
echo "============================================"
echo "  Integration Test Results"
echo "============================================"
for r in "${RESULTS[@]}"; do
  echo "  $r"
done
echo "============================================"
echo "  Total: $((PASS+FAIL+SKIP)) | Pass: $PASS | Fail: $FAIL | Skip: $SKIP"
echo "============================================"
echo ""
echo "Logs saved to: $LOGDIR"

if [[ $FAIL -gt 0 ]]; then
  exit 1
fi
