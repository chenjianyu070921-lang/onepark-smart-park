-- OnePark 用户管理服务(sys_db): 用户/角色/菜单 + RBAC 关联表
-- 由 docker-compose 的 mysql 初始化挂载执行, 或手动导入到 MySQL 实例.

CREATE DATABASE IF NOT EXISTS sys_db CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
USE sys_db;

CREATE TABLE IF NOT EXISTS sys_user (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    username    VARCHAR(64)  NOT NULL,
    password    VARCHAR(255) NOT NULL,
    nickname    VARCHAR(64)  NOT NULL DEFAULT '',
    status      TINYINT      NOT NULL DEFAULT 1,
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at  DATETIME     DEFAULT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_username (username),
    KEY idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS sys_role (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    role_key    VARCHAR(64)  NOT NULL,
    role_name   VARCHAR(64)  NOT NULL DEFAULT '',
    remark      VARCHAR(255) NOT NULL DEFAULT '',
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_role_key (role_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS sys_menu (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    parent_id   BIGINT         NOT NULL DEFAULT 0,
    menu_key    VARCHAR(64)    NOT NULL,
    menu_name   VARCHAR(64)    NOT NULL DEFAULT '',
    permission  VARCHAR(128)   NOT NULL DEFAULT '',
    path        VARCHAR(255)   NOT NULL DEFAULT '',
    sort        INT            NOT NULL DEFAULT 0,
    created_at  DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_menu_key (menu_key),
    KEY idx_parent (parent_id),
    KEY idx_permission (permission)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS sys_user_role (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id    BIGINT         NOT NULL,
    role_id    BIGINT         NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_user_role (user_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS sys_role_menu (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    role_id    BIGINT         NOT NULL,
    menu_id    BIGINT         NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_role_menu (role_id, menu_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
