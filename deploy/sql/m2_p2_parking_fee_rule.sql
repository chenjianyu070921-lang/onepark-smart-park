-- M2 停车计费规则配置表(P2 计费规则配置接口)
-- 说明: 设计文档 13.3 原计划 parking_fee_rule, 此前缺迁移; 本次补齐为全新表.
-- 约束: 仅新增表, 不改动任何既有表结构/字段/索引, 不污染现有业务数据(对齐项目硬规则).
-- 计费规则以 JSON 存储(free_minutes/hourly_fee/daily_cap), 支持生效时间窗(立即/永久).
CREATE TABLE IF NOT EXISTS parking_fee_rule (
    id             BIGINT       NOT NULL AUTO_INCREMENT,
    tenant_id      BIGINT       NOT NULL,
    rule_json      JSON         NOT NULL,
    effective_from DATETIME     NULL DEFAULT NULL,
    effective_to   DATETIME     NULL DEFAULT NULL,
    created_at     DATETIME     NOT NULL,
    updated_at     DATETIME     NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='停车计费规则配置(当前生效=时间窗覆盖 now 的最新一条)';
