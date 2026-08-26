import { useEffect, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { LayoutDashboard, ScrollText, GitBranch, Settings, LogOut, KeyRound, HeartPulse, FileSearch, Users } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/dashboard', label: '仪表盘', icon: LayoutDashboard, adminOnly: false },
  { to: '/logs', label: '日志查询', icon: ScrollText, adminOnly: false },
  { to: '/audit-logs', label: '审计日志', icon: FileSearch, adminOnly: true },
  { to: '/flow', label: '数据流向', icon: GitBranch, adminOnly: false },
  { to: '/backends', label: '后端监控', icon: HeartPulse, adminOnly: false },
  { to: '/tokens', label: 'Token 管理', icon: KeyRound, adminOnly: true },
  { to: '/users', label: '用户管理', icon: Users, adminOnly: true },
  { to: '/settings', label: '配置管理', icon: Settings, adminOnly: true },
]

export function Layout() {
  const navigate = useNavigate()
  const [username, setUsername] = useState('')
  const [role, setRole] = useState('')

  useEffect(() => {
    api
      .get<{ username: string; role: string }>('/api/auth/me')
      .then((d) => { setUsername(d.username); setRole(d.role) })
      .catch(() => {})
  }, [])

  const logout = async () => {
    try {
      await api.post('/api/auth/logout')
    } catch {
      /* 忽略 */
    }
    navigate('/login')
  }

  return (
    <div className="flex h-screen">
      {/* 侧边栏 */}
      <aside className="flex w-52 shrink-0 flex-col border-r bg-card">
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <img src="/favicon.svg" alt="logo" className="h-6 w-6" />
          <span className="text-sm font-bold">Proxy Sentinel</span>
        </div>
        <nav className="flex-1 space-y-1 p-3">
          {navItems.filter(n => !n.adminOnly || role === 'admin').map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors',
                  isActive ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                )
              }
            >
              <Icon className="h-4 w-4" />
              {label}
            </NavLink>
          ))}
        </nav>
        <div className="border-t p-3">
          <div className="mb-2 truncate px-1 text-xs text-muted-foreground">{username || '...'}</div>
          <Button variant="outline" size="sm" className="w-full" onClick={logout}>
            <LogOut className="h-3.5 w-3.5" /> 登出
          </Button>
        </div>
      </aside>
      {/* 内容区 */}
      <main className="flex-1 min-w-0 min-h-0 overflow-y-auto bg-muted/30 p-6">
        <Outlet />
      </main>
    </div>
  )
}
