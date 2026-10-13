-- =====================================================================
-- OnePark 智慧园区 - 企业知识库(kb-service)建表脚本
-- 库: kb_db   （遵循 init.sql 的"每服务独立库"规范）
--
-- 四张表:
--   kb_document  知识库文档(一篇 = 一份材料)
--   kb_chunk     文档切片(检索的最小单位, 一篇 N 段)
--   kb_qa_log    问答日志(审计 + 效果评估的数据源)
--   kb_feedback  用户反馈(点赞/点踩, 回流给效果评估)
--
-- 设计取舍(答辩会问, 先写清楚):
--   1) **切片单独成表**而不是塞进文档行: 检索只扫切片, 不必每次把整篇文档拉回内存;
--      换切片策略(按段落 / 固定长度 / 标题层级)只需重灌切片, 文档原文不动。
--   2) **不建全文索引**: 本机没有分词插件, MySQL 的 FULLTEXT 对中文要靠 ngram 解析器,
--      而检索逻辑我们放在 Go 侧(替换成向量检索时不用改 SQL)。切片表只按 (tenant_id, document_id) 走索引。
--   3) **tenant_id 全表带**: 与项目 L2 的数据隔离口径一致(网关注入 x-tenant-id, 查询必须带租户条件),
--      默认 0 表示历史/未归属数据, 租户查询自然看不见。
--   4) citations 用 JSON 存: 一次问答可能引用多段, 且每段要带出处/序号/得分, 结构会演进, 不适合拆列。
--
-- 执行: mysql -uroot < deploy/sql/kb_tables.sql   (脚本自带 CREATE DATABASE 与 USE, 可重复执行)
-- =====================================================================

CREATE DATABASE IF NOT EXISTS `kb_db` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `kb_db`;

-- ============================================================
-- kb_document  知识库文档
-- ============================================================
CREATE TABLE IF NOT EXISTS `kb_document` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`   BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID(RBAC 行级隔离维度)',
  `title`       VARCHAR(200)    NOT NULL DEFAULT '' COMMENT '文档标题',
  `source`      VARCHAR(300)    NOT NULL DEFAULT '' COMMENT '出处: docs/m5/04-xxx.md 或 工单#123',
  `category`    VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '分类: 制度 / 流程 / 技术 / 工单经验',
  `content`     MEDIUMTEXT      NOT NULL COMMENT '原文全文(含换行)',
  `chunk_count` INT             NOT NULL DEFAULT 0 COMMENT '切片数, 冗余字段便于列表展示与自检',
  `status`      TINYINT         NOT NULL DEFAULT 1 COMMENT '1启用 2停用(停用不参与检索)',
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_tenant_title` (`tenant_id`, `title`, `source`),
  KEY `idx_tenant_status` (`tenant_id`, `status`),
  KEY `idx_tenant_category` (`tenant_id`, `category`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='知识库文档';

-- ============================================================
-- kb_chunk  文档切片(检索最小单位)
-- ============================================================
CREATE TABLE IF NOT EXISTS `kb_chunk` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`   BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID(与文档一致, 避免检索时空表 JOIN)',
  `document_id` BIGINT UNSIGNED NOT NULL COMMENT '所属文档 kb_document.id',
  `seq`         INT             NOT NULL DEFAULT 0 COMMENT '文档内序号, 从 0 开始(引用时显示"第 N 段")',
  `heading`     VARCHAR(200)    NOT NULL DEFAULT '' COMMENT '所属标题(切片的路径信息, 供引用展示)',
  `content`     TEXT            NOT NULL COMMENT '切片正文',
  `char_len`    INT             NOT NULL DEFAULT 0 COMMENT '切片字符数(排查切片策略用)',
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant_doc` (`tenant_id`, `document_id`, `seq`),
  KEY `idx_doc` (`document_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='知识库文档切片';

-- ============================================================
-- kb_qa_log  问答日志
-- 为什么留 mode 与 top_score: 效果评估(第四场景)要能区分
-- "没检索到"和"检索到了但答不好" —— 前者调语料, 后者调检索/生成。
-- ============================================================
CREATE TABLE IF NOT EXISTS `kb_qa_log` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`  BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID',
  `user_id`    BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '提问人(网关注入)',
  `question`   VARCHAR(500)    NOT NULL DEFAULT '' COMMENT '用户问题',
  `answer`     MEDIUMTEXT      NOT NULL COMMENT '回答正文',
  `mode`       VARCHAR(16)     NOT NULL DEFAULT '' COMMENT 'extractive(抽取式) / llm(生成式) / no_hit(无依据拒答)',
  `citations`  JSON            NULL COMMENT '引用片段: [{chunk_id,document_id,title,source,seq,score}]',
  `top_score`  DOUBLE          NOT NULL DEFAULT 0 COMMENT '命中最高分(0 表示没有命中任何切片)',
  `elapsed_ms` INT             NOT NULL DEFAULT 0 COMMENT '耗时(毫秒)',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant_created` (`tenant_id`, `created_at`),
  KEY `idx_mode` (`mode`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='知识库问答日志';

-- ============================================================
-- kb_feedback  用户反馈(点赞/点踩)
-- ============================================================
CREATE TABLE IF NOT EXISTS `kb_feedback` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`  BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID',
  `qa_id`      BIGINT UNSIGNED NOT NULL COMMENT '关联 kb_qa_log.id',
  `vote`       TINYINT         NOT NULL DEFAULT 1 COMMENT '1有用 2没用',
  `comment`    VARCHAR(500)    NOT NULL DEFAULT '' COMMENT '补充说明',
  `created_at` DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_qa_vote` (`qa_id`, `vote`, `tenant_id`),
  KEY `idx_tenant_vote` (`tenant_id`, `vote`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='知识库问答反馈';
