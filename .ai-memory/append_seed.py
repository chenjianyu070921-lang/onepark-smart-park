# -*- coding: utf-8 -*-
import io

path = r"D:\gocode\src\onepark-smart-park\deploy\sql\sys.sql"
with io.open(path, "r", encoding="utf-8") as f:
    content = f.read()

seed = """
-- ============ 种子数据(全新环境一条命令起后可登录) ============
-- 默认账号: admin / admin123 (密码为 bcrypt, 与 auth-service 登录校验一致)
INSERT INTO sys_user (id, username, password, nickname, status)
VALUES (1, 'admin', '$2b$10$QsiOawWvp.GGozQjkSwqRuMeHMf5n7NOFTIcdxgSZSbDSVzXcfWDy', '系统管理员', 1)
ON DUPLICATE KEY UPDATE nickname = VALUES(nickname);

INSERT INTO sys_role (id, role_key, role_name, remark)
VALUES (1, 'super_admin', '超级管理员', '拥有全部菜单权限')
ON DUPLICATE KEY UPDATE role_name = VALUES(role_name);

INSERT INTO sys_user_role (user_id, role_id) VALUES (1, 1)
ON DUPLICATE KEY UPDATE role_id = VALUES(role_id);

-- 基础菜单(示例): 用户/角色/菜单 管理
INSERT INTO sys_menu (id, parent_id, menu_key, menu_name, permission, path, sort) VALUES
(1, 0, 'user', '用户管理', 'system:user:list', '/system/user', 1),
(2, 0, 'role', '角色管理', 'system:role:list', '/system/role', 2),
(3, 0, 'menu', '菜单管理', 'system:menu:list', '/system/menu', 3)
ON DUPLICATE KEY UPDATE menu_name = VALUES(menu_name);

INSERT INTO sys_role_menu (role_id, menu_id) VALUES (1, 1), (1, 2), (1, 3)
ON DUPLICATE KEY UPDATE menu_id = VALUES(menu_id);
"""

with io.open(path, "a", encoding="utf-8", newline="\n") as f:
    f.write(seed)

print("seed appended, new length:", len(content) + len(seed))
