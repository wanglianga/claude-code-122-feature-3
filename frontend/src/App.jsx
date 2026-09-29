import React, { useState } from 'react'
import { Routes, Route, NavLink, useNavigate, Navigate } from 'react-router-dom'
import { getUser, clearSession } from './api.js'
import { ToastProvider } from './components.jsx'
import Login from './pages/Login.jsx'
import Overview from './pages/Overview.jsx'
import Kegs from './pages/Kegs.jsx'
import Batches from './pages/Batches.jsx'
import Orders from './pages/Orders.jsx'
import BarPage from './pages/BarPage.jsx'
import DriverPage from './pages/DriverPage.jsx'
import WashPage from './pages/WashPage.jsx'
import RepairConfirmPage from './pages/RepairConfirmPage.jsx'
import QCPage from './pages/QCPage.jsx'
import FinancePage from './pages/FinancePage.jsx'
import NotificationsPage from './pages/NotificationsPage.jsx'

// 各角色可见的导航
const NAV = [
  { to: '/', label: '总览', roles: ['admin', 'brewery', 'qc', 'finance'] },
  { to: '/batches', label: '批次与召回', roles: ['admin', 'brewery', 'qc'] },
  { to: '/orders', label: '订单发货', roles: ['admin', 'brewery'] },
  { to: '/kegs', label: '酒桶追溯', roles: ['admin', 'brewery', 'finance', 'qc'] },
  { to: '/bar', label: '我的在店桶', roles: ['admin', 'bar_manager'] },
  { to: '/driver', label: '回收扫码', roles: ['admin', 'driver'] },
  { to: '/wash', label: '清洗检测', roles: ['admin', 'washplant'] },
  { to: '/repairs-confirm', label: '返修确认', roles: ['admin', 'brewery'] },
  { to: '/qc', label: '品控看板', roles: ['admin', 'brewery', 'qc'] },
  { to: '/finance', label: '押金财务', roles: ['admin', 'brewery', 'finance'] },
  { to: '/notifications', label: '通知中心', roles: ['admin', 'brewery', 'qc', 'finance', 'bar_manager', 'driver', 'dealer', 'washplant'] }
]

const ROLE_NAME = {
  admin: '管理员', brewery: '酒厂仓库', qc: '品控', finance: '财务',
  bar_manager: '酒吧店长', driver: '司机', washplant: '清洗厂', dealer: '经销商'
}

export default function App() {
  const user = getUser()
  return (
    <ToastProvider>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/*" element={<Shell />} />
      </Routes>
    </ToastProvider>
  )
}

function Shell() {
  const user = getUser()
  const navigate = useNavigate()
  const [, force] = useState(0)
  if (!user) return <Navigate to="/login" replace />

  const navs = NAV.filter(n => n.roles.includes(user.role))
  const logout = () => { clearSession(); navigate('/login') }

  return (
    <div className="app">
      <header className="topbar">
        <div className="logo">🍺 精酿桶<span>追溯</span>平台</div>
        <nav className="nav">
          {navs.map(n => (
            <NavLink key={n.to} to={n.to} end={n.to === '/'}
              className={({ isActive }) => isActive ? 'active' : ''}>{n.label}</NavLink>
          ))}
        </nav>
        <div className="userbox">
          <div style={{ textAlign: 'right' }}>
            <b>{user.name}</b><br />{ROLE_NAME[user.role]}
          </div>
          <button className="ghost small" onClick={logout}>退出</button>
        </div>
      </header>
      <main className="content" key={user.id + ':' + user.role}>
        <Routes>
          <Route path="/" element={<RoleHome />} />
          <Route path="/batches" element={<Batches />} />
          <Route path="/orders" element={<Orders />} />
          <Route path="/kegs" element={<Kegs />} />
          <Route path="/bar" element={<BarPage />} />
          <Route path="/driver" element={<DriverPage />} />
          <Route path="/wash" element={<WashPage />} />
          <Route path="/repairs-confirm" element={<RepairConfirmPage />} />
          <Route path="/qc" element={<QCPage />} />
          <Route path="/finance" element={<FinancePage />} />
          <Route path="/notifications" element={<NotificationsPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}

// 登录后按角色落到最常用的页面
function RoleHome() {
  const user = getUser()
  switch (user.role) {
    case 'bar_manager': return <Navigate to="/bar" replace />
    case 'driver': return <Navigate to="/driver" replace />
    case 'washplant': return <Navigate to="/wash" replace />
    case 'finance': return <Navigate to="/finance" replace />
    case 'dealer': return <Navigate to="/notifications" replace />
    default: return <Overview />
  }
}
