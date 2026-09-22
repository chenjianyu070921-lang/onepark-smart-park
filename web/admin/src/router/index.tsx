import { Navigate, type RouteObject, useLocation } from 'react-router-dom'
import { isLoggedIn } from '../stores/auth'
import AdminLayout from '../layouts/AdminLayout'
import LoginPage from '../pages/login/Login'
import HomePage from '../pages/home/Home'
import WorkorderListPage from '../pages/workorder/WorkorderList'
import PlaceholderPage from '../pages/placeholder/Placeholder'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  if (!isLoggedIn()) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />
  }
  return <>{children}</>
}

export const routes: RouteObject[] = [
  { path: '/login', element: <LoginPage /> },
  {
    path: '/',
    element: (
      <RequireAuth>
        <AdminLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/home" replace /> },
      { path: 'home', element: <HomePage /> },
      { path: 'workorder', element: <WorkorderListPage /> },
      // 尚未实现的模块统一走占位页, 菜单可见但提示建设中.
      { path: 'dispatch', element: <PlaceholderPage title="调度任务" /> },
      { path: 'visitor', element: <PlaceholderPage title="访客管理" /> },
      { path: 'parking', element: <PlaceholderPage title="停车管理" /> },
      { path: 'notice', element: <PlaceholderPage title="公告通知" /> },
      { path: 'alarm', element: <PlaceholderPage title="告警中心" /> },
      { path: 'access', element: <PlaceholderPage title="门禁管理" /> },
      { path: 'video', element: <PlaceholderPage title="视频监控" /> },
      { path: 'device', element: <PlaceholderPage title="设备列表" /> },
      { path: 'product', element: <PlaceholderPage title="产品管理" /> },
      { path: 'energy', element: <PlaceholderPage title="能耗监控" /> },
      { path: 'energy/analysis', element: <PlaceholderPage title="能耗分析" /> },
      { path: 'leasing/contract', element: <PlaceholderPage title="合同管理" /> },
      { path: 'leasing/occupancy', element: <PlaceholderPage title="出租率" /> },
      { path: 'system/users', element: <PlaceholderPage title="用户管理" /> },
      { path: 'system/roles', element: <PlaceholderPage title="角色与菜单" /> },
    ],
  },
  { path: '*', element: <Navigate to="/home" replace /> },
]
