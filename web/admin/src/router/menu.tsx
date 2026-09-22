import type { ReactNode } from 'react'
import {
  HomeOutlined,
  ToolOutlined,
  TeamOutlined,
  CarOutlined,
  SoundOutlined,
  AlertOutlined,
  VideoCameraOutlined,
  ClusterOutlined,
  FundOutlined,
  FileTextOutlined,
  ApartmentOutlined,
  SettingOutlined,
} from '@ant-design/icons'

export interface MenuNode {
  key: string
  label: string
  icon?: ReactNode
  path?: string
  children?: MenuNode[]
  /**
   * RBAC 过滤预留: 后续接入 /api/roles 把 roleIds 映射为 role_key 后,
   * 未列出的角色将被过滤(空数组 = 全部角色可见).
   */
  roles?: string[]
}

// 菜单与路由表. 已实现的页面挂真实组件, 其余指向占位页, 后续里程碑逐个替换.
export const menuConfig: MenuNode[] = [
  { key: 'home', label: '工作台', icon: <HomeOutlined />, path: '/home' },
  {
    key: 'property',
    label: '物业运营',
    icon: <ToolOutlined />,
    children: [
      { key: 'workorder', label: '工单管理', path: '/workorder' },
      { key: 'dispatch', label: '调度任务', path: '/dispatch' },
    ],
  },
  {
    key: 'passage',
    label: '智慧通行',
    icon: <TeamOutlined />,
    children: [
      { key: 'visitor', label: '访客管理', path: '/visitor' },
      { key: 'parking', label: '停车管理', path: '/parking' },
      { key: 'notice', label: '公告通知', path: '/notice' },
    ],
  },
  {
    key: 'security',
    label: '综合安防',
    icon: <AlertOutlined />,
    children: [
      { key: 'alarm', label: '告警中心', path: '/alarm' },
      { key: 'access', label: '门禁管理', path: '/access' },
      { key: 'video', label: '视频监控', path: '/video' },
    ],
  },
  {
    key: 'device',
    label: '设备接入',
    icon: <ClusterOutlined />,
    children: [
      { key: 'device-list', label: '设备列表', path: '/device' },
      { key: 'product', label: '产品管理', path: '/product' },
    ],
  },
  {
    key: 'energy',
    label: '能耗楼宇',
    icon: <FundOutlined />,
    children: [
      { key: 'energy-monitor', label: '能耗监控', path: '/energy' },
      { key: 'energy-analysis', label: '能耗分析', path: '/energy/analysis' },
    ],
  },
  {
    key: 'leasing',
    label: '招商租赁',
    icon: <ApartmentOutlined />,
    children: [
      { key: 'contract', label: '合同管理', path: '/leasing/contract' },
      { key: 'occupancy', label: '出租率', path: '/leasing/occupancy' },
    ],
  },
  {
    key: 'system',
    label: '系统管理',
    icon: <SettingOutlined />,
    children: [
      { key: 'users', label: '用户管理', path: '/system/users' },
      { key: 'roles', label: '角色与菜单', path: '/system/roles' },
    ],
  },
]

// 图标按需分配给占位页详情展示(避免未使用 import 报错, 统一从此处导出).
export const placeholderIcons = {
  dispatch: <ToolOutlined />,
  visitor: <TeamOutlined />,
  parking: <CarOutlined />,
  notice: <SoundOutlined />,
  alarm: <AlertOutlined />,
  video: <VideoCameraOutlined />,
  contract: <FileTextOutlined />,
}
