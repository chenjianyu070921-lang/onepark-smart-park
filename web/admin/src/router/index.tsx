import { Navigate, type RouteObject, useLocation } from 'react-router-dom'
import { isLoggedIn } from '../stores/auth'
import AdminLayout from '../layouts/AdminLayout'
import LoginPage from '../pages/login/Login'
import HomePage from '../pages/home/Home'
import WorkorderListPage from '../pages/workorder/WorkorderList'
import NoticeListPage from '../pages/notice/NoticeList'
import VisitorListPage from '../pages/visitor/VisitorList'
import ParkingPage from '../pages/parking/Parking'
import AlarmCenterPage from '../pages/alarm/AlarmCenter'
import ContractListPage from '../pages/leasing/ContractList'
import OccupancyPage from '../pages/leasing/Occupancy'
import RentBillListPage from '../pages/billing/RentBillList'
import EnergyAnalysisPage from '../pages/energy/EnergyAnalysis'
import UsersPage from '../pages/system/Users'
import RolesPage from '../pages/system/Roles'
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
      { path: 'visitor', element: <VisitorListPage /> },
      { path: 'parking', element: <ParkingPage /> },
      { path: 'notice', element: <NoticeListPage /> },
      { path: 'alarm', element: <AlarmCenterPage /> },
      { path: 'access', element: <PlaceholderPage title="门禁管理" /> },
      { path: 'video', element: <PlaceholderPage title="视频监控" /> },
      { path: 'device', element: <PlaceholderPage title="设备列表" /> },
      { path: 'product', element: <PlaceholderPage title="产品管理" /> },
      { path: 'energy', element: <PlaceholderPage title="能耗监控" /> },
      { path: 'energy/analysis', element: <EnergyAnalysisPage /> },
      { path: 'leasing/contract', element: <ContractListPage /> },
      { path: 'leasing/occupancy', element: <OccupancyPage /> },
      { path: 'billing', element: <RentBillListPage /> },
      { path: 'system/users', element: <UsersPage /> },
      { path: 'system/roles', element: <RolesPage /> },
    ],
  },
  { path: '*', element: <Navigate to="/home" replace /> },
]
