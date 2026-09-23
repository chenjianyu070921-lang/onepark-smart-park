-- 菜单种子与前端侧边栏对齐(增量迁移, 幂等可重复执行)
-- 背景:
--   sys.sql 的初始菜单是"单根 sys 树 + resource:action 权限节点", menu_key 与前端
--   web/admin/src/router/menu.tsx 的 key 完全对不上, 直接拿后端菜单渲染侧边栏会导致
--   所有菜单消失. 本脚本把前端的菜单结构补进 sys_menu, 让 GET /api/menus?role_id=
--   能返回"该角色可见的前端菜单".
--
-- 设计约束(重要):
--   1) 复用不重建: gateway/internal/middleware/permission.go 的 RBAC 注册表依赖
--      user:write / workorder:read / alarm:confirm 等 permission, 这些 permission
--      挂在旧菜单行上, 删行会让网关校验全部 403. 因此旧行只 UPDATE 不 DELETE.
--   2) 旧权限节点(user:write / alarm:confirm 等)继续保留, 它们对前端不可见 —— 前端
--      只保留 menu_key 命中自身 menuConfig 的节点, 其余自动忽略.
--   3) 已存在的同名 menu_key(workorder/parking/alarm/access)直接改造为前端节点,
--      permission 保持不变, 兼顾网关 RBAC 与前端展示.
--
-- 执行: mysql -h<host> -u<user> -p sys_db < m6_menu_frontend_align.sql

USE sys_db;

-- ============ 1. 目录节点(前端一级分组) ============
-- system 复用旧 sys 根行(id=1): 改 menu_key/menu_name/path, 保持其下旧子节点的父关系不变.
INSERT INTO sys_menu (id, parent_id, menu_key, menu_name, permission, path, sort) VALUES
    (1,   0,   'system',  '系统管理', '', '/system',          80),
    (100, 0,   'home',    '工作台',   'home:read', '/home',    1),
    (101, 0,   'property','物业运营', '', '',                 10),
    (102, 0,   'passage', '智慧通行', '', '',                 20),
    (103, 0,   'security','综合安防', '', '',                 30),
    (104, 0,   'device',  '设备接入', '', '',                 40),
    (105, 0,   'energy',  '能耗楼宇', '', '',                 50),
    (106, 0,   'leasing', '招商租赁', '', '',                 60)
ON DUPLICATE KEY UPDATE
    parent_id = VALUES(parent_id),
    menu_name = VALUES(menu_name),
    permission = VALUES(permission),
    path       = VALUES(path),
    sort       = VALUES(sort);

-- ============ 2. 页面节点(新增) ============
INSERT INTO sys_menu (id, parent_id, menu_key, menu_name, permission, path, sort) VALUES
    (110, 101, 'dispatch',        '调度任务', 'dispatch:read', '/dispatch',         12),
    (111, 102, 'visitor',         '访客管理', 'visitor:read',  '/visitor',          21),
    (112, 102, 'notice',          '公告通知', 'notice:read',   '/notice',           23),
    (113, 103, 'video',           '视频监控', 'video:read',    '/video',            33),
    (114, 104, 'device-list',     '设备列表', 'device:read',   '/device',           41),
    (115, 104, 'product',         '产品管理', 'product:read',  '/product',          42),
    (116, 105, 'energy-monitor',  '能耗监控', 'energy:read',   '/energy',           51),
    (117, 105, 'energy-analysis', '能耗分析', 'energy:read',   '/energy/analysis',  52),
    (118, 106, 'contract',        '合同管理', 'lease:read',    '/leasing/contract', 61),
    (119, 106, 'occupancy',       '出租率',   'lease:read',    '/leasing/occupancy',62),
    (120, 106, 'billing',         '账单管理', 'lease:read',    '/billing',          63),
    (121, 1,   'users',           '用户管理', 'user:read',     '/system/users',     81),
    (122, 1,   'roles',           '角色与菜单','role:read',    '/system/roles',     82)
ON DUPLICATE KEY UPDATE
    parent_id = VALUES(parent_id),
    menu_name = VALUES(menu_name),
    permission = VALUES(permission),
    path       = VALUES(path),
    sort       = VALUES(sort);

-- ============ 3. 复用旧权限节点为侧边栏节点 ============
-- 这几个 key 在旧种子里已存在(承载网关 RBAC 的 permission), 这里只改归属与展示信息,
-- permission 一字不动, 否则网关注册表(user:write/workorder:read 等)会失配.
SET @property = (SELECT id FROM sys_menu WHERE menu_key = 'property');
SET @passage  = (SELECT id FROM sys_menu WHERE menu_key = 'passage');
SET @security = (SELECT id FROM sys_menu WHERE menu_key = 'security');

UPDATE sys_menu SET parent_id = @property, menu_name = '工单管理', path = '/workorder', sort = 11
    WHERE menu_key = 'workorder';
UPDATE sys_menu SET parent_id = @passage,  menu_name = '停车管理', path = '/parking',  sort = 22
    WHERE menu_key = 'parking';
UPDATE sys_menu SET parent_id = @security, menu_name = '告警中心', path = '/alarm',    sort = 31
    WHERE menu_key = 'alarm';
UPDATE sys_menu SET parent_id = @security, menu_name = '门禁管理', path = '/access',   sort = 32
    WHERE menu_key = 'access';

-- ============ 4. 角色-菜单绑定 ============
-- super_admin: 全量(含旧权限节点, 保证网关写操作不被拦)
INSERT IGNORE INTO sys_role_menu (role_id, menu_id)
SELECT r.id, m.id FROM sys_role r CROSS JOIN sys_menu m WHERE r.role_key = 'super_admin';

-- park_admin(园区管理员): 工作台 + 物业/通行/安防/能耗/租赁 + 系统管理
INSERT IGNORE INTO sys_role_menu (role_id, menu_id)
SELECT r.id, m.id FROM sys_role r JOIN sys_menu m
WHERE r.role_key = 'park_admin' AND m.menu_key IN (
    'home','property','workorder','dispatch','passage','visitor','parking','notice',
    'security','alarm','access','video','energy','energy-monitor','energy-analysis',
    'leasing','contract','occupancy','billing','system','users','roles'
);

-- security(保安): 安防域 + 工作台
INSERT IGNORE INTO sys_role_menu (role_id, menu_id)
SELECT r.id, m.id FROM sys_role r JOIN sys_menu m
WHERE r.role_key = 'security' AND m.menu_key IN (
    'home','security','alarm','access','video'
);

-- property_mgr(物业经理): 物业 + 通行域
INSERT IGNORE INTO sys_role_menu (role_id, menu_id)
SELECT r.id, m.id FROM sys_role r JOIN sys_menu m
WHERE r.role_key = 'property_mgr' AND m.menu_key IN (
    'home','property','workorder','passage','visitor','parking','notice'
);

-- staff(普通员工): 工作台 + 公告
INSERT IGNORE INTO sys_role_menu (role_id, menu_id)
SELECT r.id, m.id FROM sys_role r JOIN sys_menu m
WHERE r.role_key = 'staff' AND m.menu_key IN ('home','notice');
