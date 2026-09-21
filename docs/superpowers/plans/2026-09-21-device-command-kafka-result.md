# Device Command Kafka Result Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a durable command-result pipeline with Kafka acknowledgement, timeout scanning, and automatic retry for both timeout and explicit device failure.

**Architecture:** Keep `command_log` as the authoritative command state machine, add a shared Kafka `CommandResult` contract, produce results from both EMQX dispatch and TCP gateway, and consume them in device-service with conditional database updates. Retry ownership moves from Redis counters to an atomic `retry_count` database update.

**Tech Stack:** Go 1.26, go-zero, GORM/MySQL, segmentio/kafka-go, Paho MQTT, JUnit-style Go tests.

## Global Constraints

- Final code delivery is blocked by the AGENTS.md code-explain gate.
- Do not output full implementation code before the user passes the required questions.
- Preserve existing statuses: `0 pending`, `1 sent`, `2 success`, `3 failed`, `4 timeout`.
- Use `CommandMaxResend=2`, `CommandTimeoutSec=30`, `TimeoutScanIntervalSec=15`, and `TimeoutScanBatchLimit=200` as defaults.
- Kafka topic: `device-command-result`; consumer group: `device-service-command-result`.
- Kafka message key: `device_id`.
- Add the SQL migration before relying on the new `retry_count` column.

---

### Task 1: Shared Kafka contract and command-log state model

**Files:**
- Modify: `common/kafka/topics.go`
- Modify: `common/kafka/contract.go`
- Modify: `app/device-service/internal/model/command_log.go`
- Modify: `app/device-service/internal/model/command_log_model.go`
- Create: `deploy/sql/m1_command_result_retry_migration.sql`
- Modify: `deploy/sql/m1_mysql_tables.sql`
- Test: `common/kafka/contract_test.go`
- Test: `app/device-service/internal/model/command_log_test.go`

**Interfaces:**
- Produces: `kafka.CommandResult`
- Produces: `kafka.TopicDeviceCommandResult`
- Produces: `kafka.GroupDeviceCommandResult`
- Produces model methods:
  - `RecordSuccess(ctx context.Context, requestID string, response []byte, executedAt time.Time) error`
  - `RecordFailureForRetry(ctx context.Context, requestID string, response []byte, executedAt time.Time, maxRetry int) error`
  - `FindRetryList(ctx context.Context, limit int) ([]*CommandLog, error)`
  - `ClaimRetry(ctx context.Context, requestID string, timeoutAt time.Time, maxRetry int) (bool, error)`
  - `FinishTimeout(ctx context.Context, requestID string) error`

**Steps:**
- [ ] Write failing contract tests for JSON field names, status constants, and topic/group values.
- [ ] Write failing model tests for the expected state transitions and SQL predicates where practical without a live MySQL.
- [ ] Add the shared `CommandResult` contract and topic/group constants.
- [ ] Add `CommandLog.RetryCount` and the migration SQL.
- [ ] Implement conditional model updates:
  - success only from `pending/sent`;
  - failed remains `sent` with immediate retry deadline while below the limit;
  - failed becomes terminal when at or above the limit;
  - retry claim increments `retry_count` only while below the limit;
  - timeout finalization only affects `pending/sent`.
- [ ] Run:
  - `go test ./...` in `common`
  - `go test ./internal/model` in `app/device-service`
- [ ] Commit: `feat(common): add command result contract`

---

### Task 2: device-service Kafka result consumer

**Files:**
- Create: `app/device-service/internal/mq/command_result.go`
- Create: `app/device-service/internal/mq/command_result_test.go`
- Modify: `app/device-service/device.go`
- Test: `app/device-service/internal/mq/command_result_test.go`

**Interfaces:**
- Consumes: `svc.ServiceContext`, `kafka.Message`, and Task 1 model methods.
- Produces: `CommandResultHandler.Handle(ctx context.Context, msg kafka.Message) error`
- Produces: `CommandResultHandler.Consume(ctx context.Context, brokers string) error`

**Steps:**
- [ ] Write failing tests covering:
  - valid success result records success;
  - valid failed result records retryable failure;
  - missing fields and invalid JSON return nil;
  - unknown request ID returns nil;
  - device mismatch returns nil;
  - database error is returned for Kafka retry.
- [ ] Implement result parsing, status normalization, response normalization, and conditional model dispatch.
- [ ] Start the second Kafka consumer in `device.go` using the existing task context.
- [ ] Run `go test ./internal/mq` in `app/device-service`.
- [ ] Commit: `feat(device): consume command results from Kafka`

---

### Task 3: Durable timeout and retry scanner

**Files:**
- Modify: `app/device-service/internal/logic/devicecommandlogic.go`
- Modify: `app/device-service/internal/cron/commandtimeout.go`
- Create: `app/device-service/internal/cron/commandtimeout_test.go`
- Test: `app/device-service/internal/cron/commandtimeout_test.go`

**Interfaces:**
- Consumes: Task 1 `ClaimRetry`, `FindRetryList`, and `FinishTimeout`.
- Produces: unchanged `CommandTimeoutTask.Start(ctx context.Context)` API.
- Produces: `scanOnce(ctx context.Context)` for tests.

**Steps:**
- [ ] Write failing tests with fake command model and MQTT publisher covering:
  - retry is claimed before publish;
  - claim conflict does not publish;
  - publish failure is persisted as a retryable error;
  - no downlink finalizes timeout;
  - explicit failed response finalizes as failed after retry exhaustion;
  - successful result is never resent.
- [ ] Change initial MQTT publish failure to retain retryable state rather than terminal failure.
- [ ] Remove Redis retry counting and use atomic DB claims.
- [ ] Keep the downlink payload shape and `resend:true` marker for retries.
- [ ] Run `go test ./internal/cron ./internal/logic` in `app/device-service`.
- [ ] Commit: `feat(device): persist command retry state`

---

### Task 4: EMQX result producer in event-dispatcher

**Files:**
- Modify: `app/event-dispatcher/internal/dispatch/dispatch.go`
- Modify: `app/event-dispatcher/internal/dispatch/dispatch_test.go`
- Test: `app/event-dispatcher/internal/dispatch/dispatch_test.go`

**Interfaces:**
- Consumes: `kafka.CommandResult` and `kafka.TopicDeviceCommandResult`.
- Produces: support for MQTT kind `cmd-result`.

**Steps:**
- [ ] Write failing dispatch tests for:
  - `onepark/device/{productKey}/{deviceId}/cmd-result` publishes one result message;
  - result key is `device_id`;
  - request ID, status, response, occurred time, and source are preserved;
  - invalid result payload goes to DLQ;
  - unknown device goes to DLQ;
  - Kafka failure retries and eventually uses DLQ.
- [ ] Add `cmd-result` parsing without changing telemetry/event/status behavior.
- [ ] Reuse the existing archive resolver and `publishWithRetry` path.
- [ ] Run `go test ./...` in `app/event-dispatcher`.
- [ ] Commit: `feat(dispatch): publish command results to Kafka`

---

### Task 5: TCP gateway Kafka ACK producer

**Files:**
- Modify: `app/gateway-service/internal/frame/handler.go`
- Modify: `app/gateway-service/internal/svc/servicecontext.go`
- Modify: `app/gateway-service/internal/model/model.go`
- Create or modify: `app/gateway-service/internal/frame/handler_test.go`
- Test: `app/gateway-service/internal/frame/handler_test.go`

**Interfaces:**
- Consumes: `kafka.CommandResult` and `kafka.TopicDeviceCommandResult`.
- Removes: gateway `CommandLogModel` direct database dependency.

**Steps:**
- [ ] Write failing tests for:
  - `success` ACK publishes a success result;
  - `failed`/`error` ACK publishes a failed result;
  - request ID is required;
  - response payload is preserved;
  - Kafka publish error returns an error to the connection handler.
- [ ] Replace `finishCommand` DB update with Kafka publish.
- [ ] Remove command-log model initialization and interface from gateway service context.
- [ ] Run `go test ./...` in `app/gateway-service`.
- [ ] Commit: `feat(gateway): publish ACK results to Kafka`

---

### Task 6: End-to-end verification and gate artifacts

**Files:**
- Modify: `docs/walkthrough/2026-09-21-device-command-kafka-result.md`
- Modify: `docs/walkthrough/diagnosis/2026-09-21-device-command-kafka-result.md`
- Modify: `docs/walkthrough/learning-profile.md`

**Interfaces:**
- Consumes all implementation tasks.
- Produces the required AGENTS.md walkthrough and diagnosis records.

**Steps:**
- [ ] Run module tests:
  - `go test ./...` in `common`
  - `go test ./...` in `app/device-service`
  - `go test ./...` in `app/event-dispatcher`
  - `go test ./...` in `app/gateway-service`
- [ ] Run `go test ./...` from repository root using the Go workspace.
- [ ] Inspect `git diff` for accidental unrelated changes.
- [ ] Generate three to five code-specific gate questions.
- [ ] Wait for user answers and evaluate them.
- [ ] Only after passing the gate, write and commit the walkthrough card, diagnosis, and learning-profile update.
- [ ] Commit: `feat(device): verify command result retry pipeline`

## Self-Review

- Spec coverage: contract, DB retry state, consumer, EMQX producer, TCP producer, timeout behavior, failure retry, SQL migration, and tests are all represented.
- Placeholder scan: no TBD/TODO or undefined follow-up work is intentionally left.
- Type consistency: producer and consumer tasks both rely on `kafka.CommandResult`; scanner and consumer rely on the same conditional model methods.
