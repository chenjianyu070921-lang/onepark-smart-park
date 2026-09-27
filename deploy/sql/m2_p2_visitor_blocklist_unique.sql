-- 访客黑名单并发去重防线(审查问题2): 同一租户下, 同一手机号/身份证号仅允许一条"生效"记录.
-- 背景: addvisitorblocklistlogic.go 为"先查后写", 多岗并发录入同维度会被拉黑两次, 产生重复生效记录.
-- 方案: 引入生成列(维度为空时为 NULL) + 唯一索引. MySQL 唯一索引不约束 NULL, 因此:
--   1) 允许仅按单维度(手机号或身份证)拉黑的多条记录共存;
--   2) 同一维度被"解除(status=2)"后生成列为 NULL 释放槽位, 可再次拉黑.
-- 生成列 + 唯一索引在 MySQL 8.0 支持; ADD COLUMN / CREATE INDEX 均 IF NOT EXISTS, 可重复执行.
ALTER TABLE visitor_blocklist
  ADD COLUMN IF NOT EXISTS phone_key  VARCHAR(20) GENERATED ALWAYS AS (NULLIF(phone, '')) VIRTUAL,
  ADD COLUMN IF NOT EXISTS id_no_key VARCHAR(64) GENERATED ALWAYS AS (NULLIF(id_no, '')) VIRTUAL;

CREATE UNIQUE INDEX IF NOT EXISTS uk_blocklist_phone ON visitor_blocklist (tenant_id, phone_key);
CREATE UNIQUE INDEX IF NOT EXISTS uk_blocklist_idno  ON visitor_blocklist (tenant_id, id_no_key);
