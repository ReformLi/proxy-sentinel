import { useCallback, useEffect, useState } from 'react'
import { KeyRound, Plus, RefreshCw, Trash2, Lock } from 'lucide-react'
import { api, type UserListData } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Dialog } from '@/components/ui/dialog'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/** 用户管理：管理后台登录账号 */
export default function Users() {
  const [data, setData] = useState<UserListData | null>(null)
  const [msg, setMsg] = useState('')
  const [dialogMsg, setDialogMsg] = useState('')
  const [busy, setBusy] = useState(false)

  // 新建用户
  const [showCreate, setShowCreate] = useState(false)
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newRole, setNewRole] = useState('viewer')

  // 重置密码
  const [resetTarget, setResetTarget] = useState<{ id: number; username: string } | null>(null)
  const [resetPassword, setResetPassword] = useState('')

  const load = useCallback(async () => {
    setBusy(true)
    setMsg('')
    try {
      const d = await api.get<UserListData>('/api/users')
      setData(d)
    } catch (e: any) {
      setMsg(e.message || '加载失败')
    } finally {
      setBusy(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const handleCreate = async () => {
    if (!newUsername.trim() || !newPassword) {
      setDialogMsg('用户名和密码不能为空')
      return
    }
    if (newUsername.trim().length < 3) {
      setDialogMsg('用户名至少 3 个字符')
      return
    }
    if (newPassword.length < 6) {
      setDialogMsg('密码至少 6 位')
      return
    }
    setBusy(true)
    setDialogMsg('')
    try {
      await api.post('/api/users', { username: newUsername.trim(), password: newPassword, role: newRole })
      setShowCreate(false)
      setNewUsername('')
      setNewPassword('')
      setNewRole('viewer')
      await load()
      setMsg('用户创建成功')
    } catch (e: any) {
      setDialogMsg(e.message || '创建失败')
    } finally {
      setBusy(false)
    }
  }

  const handleDelete = async (id: number, username: string) => {
    if (!window.confirm(`确定删除用户 "${username}"？此操作不可撤销。`)) return
    setBusy(true)
    setMsg('')
    try {
      await api.delete(`/api/users/${id}`)
      await load()
      setMsg(`用户 "${username}" 已删除`)
    } catch (e: any) {
      setMsg(e.message || '删除失败')
    } finally {
      setBusy(false)
    }
  }

  const handleResetPassword = async () => {
    if (!resetTarget) return
    if (resetPassword.length < 6) {
      setDialogMsg('密码至少 6 位')
      return
    }
    setBusy(true)
    setDialogMsg('')
    try {
      await api.put(`/api/users/${resetTarget.id}/password`, { password: resetPassword })
      setResetTarget(null)
      setResetPassword('')
      setMsg(`用户 "${resetTarget.username}" 密码已重置`)
    } catch (e: any) {
      setDialogMsg(e.message || '重置失败')
    } finally {
      setBusy(false)
    }
  }

  const fmtTime = (s: string) => {
    try { return new Date(s).toLocaleString('zh-CN') } catch { return s }
  }

  const users = data?.users ?? []
  const currentUser = data?.current_user ?? ''
  const currentRole = data?.current_role ?? 'admin'
  const isAdmin = currentRole !== 'viewer'

  return (
    <div className="space-y-6 p-6">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <div>
            <CardTitle className="flex items-center gap-2">
              <KeyRound className="h-5 w-5" />
              用户管理
            </CardTitle>
            <CardDescription>后台登录账号的增删与密码重置</CardDescription>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={load} disabled={busy}>
              <RefreshCw className="h-4 w-4" />
              刷新
            </Button>
            {isAdmin && (
              <Button size="sm" onClick={() => { setDialogMsg(''); setShowCreate(true) }}>
                <Plus className="h-4 w-4" />
                新建用户
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent>
          {msg && (
            <div className={`mb-4 rounded-md px-3 py-2 text-sm ${msg.includes('失败') || msg.includes('不能') ? 'bg-destructive/10 text-destructive' : 'bg-primary/10 text-primary'}`}>
              {msg}
            </div>
          )}

          {/* 用户列表 */}
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-muted-foreground">
                  <th className="pb-2 pr-4 font-medium">ID</th>
                  <th className="pb-2 pr-4 font-medium">用户名</th>
                  <th className="pb-2 pr-4 font-medium">角色</th>
                  <th className="pb-2 pr-4 font-medium">创建时间</th>
                  <th className="pb-2 pr-4 font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {users.length === 0 && !busy && (
                  <tr><td colSpan={5} className="py-4 text-center text-muted-foreground">暂无用户数据</td></tr>
                )}
                {users.map(u => (
                  <tr key={u.id} className="border-b last:border-0">
                    <td className="py-3 pr-4 text-muted-foreground">{u.id}</td>
                    <td className="py-3 pr-4 font-medium">
                      {u.username}
                      {u.username === currentUser && (
                        <Badge variant="secondary" className="ml-2">当前登录</Badge>
                      )}
                    </td>
                    <td className="py-3 pr-4">
                      <Badge variant={u.role === 'admin' ? 'default' : 'secondary'}>{u.role}</Badge>
                    </td>
                    <td className="py-3 pr-4 text-muted-foreground">{fmtTime(u.created_at)}</td>
                    <td className="py-3 pr-4">
                      {isAdmin && (
                        <div className="flex gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => { setResetTarget({ id: u.id, username: u.username }); setResetPassword(''); setDialogMsg('') }}
                          >
                            <Lock className="h-3.5 w-3.5" />
                            重置密码
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-destructive hover:text-destructive"
                            disabled={u.username === currentUser}
                          title={u.username === currentUser ? '不能删除自己' : ''}
                          onClick={() => handleDelete(u.id, u.username)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          删除
                        </Button>
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {/* 新建用户弹窗 */}
      <Dialog open={showCreate} onClose={() => setShowCreate(false)} title="新建用户">
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">创建一个新的后台登录账号</p>
          <div>
            <label className="mb-1 block text-sm text-muted-foreground">用户名</label>
            <Input
              value={newUsername}
              onChange={e => setNewUsername(e.target.value)}
              placeholder="3~32 字符"
              maxLength={32}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm text-muted-foreground">密码</label>
            <Input
              type="password"
              value={newPassword}
              onChange={e => setNewPassword(e.target.value)}
              placeholder="至少 6 位"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm text-muted-foreground">角色</label>
            <select
              className="w-full rounded border bg-background px-3 py-2 text-sm"
              value={newRole}
              onChange={e => setNewRole(e.target.value)}
            >
              <option value="viewer">viewer（只读）</option>
              <option value="admin">admin（完全控制）</option>
            </select>
          </div>
          {dialogMsg && <div className="text-sm text-destructive">{dialogMsg}</div>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setShowCreate(false)}>取消</Button>
            <Button onClick={handleCreate} disabled={busy}>创建</Button>
          </div>
        </div>
      </Dialog>

      {/* 重置密码弹窗 */}
      <Dialog open={!!resetTarget} onClose={() => setResetTarget(null)} title="重置密码">
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            为用户 "{resetTarget?.username}" 设置新密码
          </p>
          <div>
            <label className="mb-1 block text-sm text-muted-foreground">新密码</label>
            <Input
              type="password"
              value={resetPassword}
              onChange={e => setResetPassword(e.target.value)}
              placeholder="至少 6 位"
              onKeyDown={e => { if (e.key === 'Enter') handleResetPassword() }}
            />
          </div>
          {dialogMsg && <div className="text-sm text-destructive">{dialogMsg}</div>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setResetTarget(null)}>取消</Button>
            <Button onClick={handleResetPassword} disabled={busy}>确认重置</Button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
