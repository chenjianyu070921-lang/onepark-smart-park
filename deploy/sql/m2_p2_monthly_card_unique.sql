-- 停车月卡并发去重防线(审查问题3): 同一租户同一车牌仅允许一条"生效(status=1)"月卡.
-- 背景: createmonthlycardlogic.go 为"先查后写", 并发办卡会插入重复生效月卡; 管理端出现重复记录.
-- 方案: 引入生成列(仅 status=1 时取车牌, 否则 NULL) + 唯一索引. MySQL 唯一索引不约束 NULL, 因此:
--   1) 停用(status=2)的月卡生成列为 NULL, 不占唯一槽位, 允许"停用后重新办卡";
--   2) 续费为原地更新(end_time/status), 不新增行, 硬唯一不会阻断续费;
--   3) 仅约束"生效"态, 过期但仍 status=1 的卡由业务层时间窗识别(见 createmonthlycardlogic.go).
-- 生成列 + 唯一索引在 MySQL 8.0 支持; ADD COLUMN / CREATE INDEX 均 IF NOT EXISTS, 可重复执行.
ALTER TABLE monthly_card
  ADD COLUMN IF NOT EXISTS active_plate_key VARCHAR(32)
    GENERATED ALWAYS AS (NULLIF(CASE WHEN status = 1 THEN plate_no ELSE NULL END, '')) VIRTUAL;

CREATE UNIQUE INDEX IF NOT EXISTS uk_monthly_active_plate ON monthly_card (tenant_id, active_plate_key);
