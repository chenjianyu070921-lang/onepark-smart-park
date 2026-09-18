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
    data_scope  TINYINT      NOT NULL DEFAULT 2,  -- 数据权限范围: 1全部 2本园区/租户 3本部门 4本人 5自定义(与 SysRole.DataScope 对齐)
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

-- ============================================================
-- RBAC 初始种子数据(幂等: INSERT IGNORE 依赖唯一键, 容器重启不重复写入)
-- 三级映射: 用户(sys_user_role) -> 角色(sys_role) -> 菜单权限(sys_role_menu -> sys_menu.permission)
-- 说明: 下方已内置一个初始管理员(admin / super_admin)用于一键引导, 首次登录请改密或轮换;
-- 后续账号请通过 UserCreate 接口创建并 RoleAssign 分配角色(避免 SQL 硬编码更多密码).
-- ============================================================

-- 角色目录
INSERT IGNORE INTO sys_role (id, role_key, role_name, data_scope, remark) VALUES
    (1, 'super_admin', '超级管理员', 1, '全部数据权限, 拥有所有菜单权限'),
    (2, 'park_admin',  '园区管理员', 2, '本园区/租户数据权限'),
    (3, 'staff',       '普通员工',   4, '仅本人数据权限');

-- 菜单/权限目录(permission 为空表示仅目录节点, 不含具体操作权限)
INSERT IGNORE INTO sys_menu (id, parent_id, menu_key, menu_name, permission, path, sort) VALUES
    (1, 0, 'sys',        '系统管理',   '',          '/sys',            1),
    (2, 1, 'user',       '用户管理',   'user:read', '/sys/user',       2),
    (3, 1, 'user:write', '用户编辑',   'user:write','/sys/user/edit',  3),
    (4, 1, 'role',       '角色管理',   'role:read', '/sys/role',       4),
    (5, 1, 'role:write', '角色编辑',   'role:write','/sys/role/edit',  5),
    (6, 1, 'menu',       '菜单权限',   'menu:read', '/sys/menu',       6),
    (7, 1, 'menu:write', '菜单编辑',   'menu:write','/sys/menu/edit',  7);

-- 角色-菜单绑定: super_admin 拥有全部; park_admin 仅只读节点; staff 仅用户查看
INSERT IGNORE INTO sys_role_menu (role_id, menu_id) VALUES
    (1, 1), (1, 2), (1, 3), (1, 4), (1, 5), (1, 6), (1, 7),
    (2, 1), (2, 2), (2, 4), (2, 6),
    (3, 2);

-- ============================================================
-- 初始管理员种子账号(一键引导, 首次登录务必改密)
-- SQL 无法计算 bcrypt, 此处内置默认密码 "Admin@123456" 的 bcrypt 哈希(成本 10);
-- 用户名 admin 绑定 super_admin(role_id=1). 上线后通过 UserCreate + RoleAssign 轮换或改密.
-- ============================================================
INSERT IGNORE INTO sys_user (id, username, password, nickname, status) VALUES
    (1, 'admin', '$2a$10$iVD2oWLuF7R9NKuu9fsUjug8cogc/QAfjgaOA7mblMsJysZsONQgK', '系统管理员', 1);
INSERT IGNORE INTO sys_user_role (user_id, role_id) VALUES
    (1, 1);
