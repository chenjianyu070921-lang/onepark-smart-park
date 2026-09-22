-- device-service command result retry state migration
-- Execute after deploying the shared code contract and before starting the new scanner.
USE `device_db`;

ALTER TABLE `command_log`
  ADD COLUMN `retry_count` INT NOT NULL DEFAULT 0 COMMENT '已重发次数(不含首次下发)' AFTER `response`;

CREATE INDEX `idx_retry_scan` ON `command_log` (`status`, `timeout_at`, `retry_count`);
