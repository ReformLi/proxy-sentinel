import { useCallback, useEffect, useState } from 'react'
import { Copy, KeyRound, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { api, type CreatedToken, type TokenListData } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Dialog } from '@/components/ui/dialog'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/** Token 管理：代理接口 Bearer Token 的增删改、独立限流、最后使用时间 */
export default function Tokens() {
  const [data, setData] = useState<TokenListData | null>(null)
  const [msg, setMsg] = useState('')
  const [revokeTarget, setRevokeTarget] = useState<{ id: number; name: string } | null>(null)
  const [busy, setBusy] = useState(false)

  // 新建表单
  const [newName, setNewName] = useState('')
  const [newRpm, setNewRpm] = useState('')
  const [customToken, setCustomToken] = useState('')
  const [showCustom, setShowCustom] = useState(false)
  const [expiryPreset, setExpiryPreset] = useState<string>('')  // '' / '7' / '30' / '90' / 'custom'
  const [expiryCustom, setExpiryCustom] = useState<string>('')  // 自定义天数（仅 preset=custom 时生效）

  // 新建成功后的一次性明文展示
  const [created, setCreated] = useState<CreatedToken | null>(null)
  const [copied, setCopied] = useState(false)

  // 行内编辑（id → { name, rpm }）
  const [editing, setEditing] = useState<Record<number, { name: string; rpm: string }>>({})

  const load = useCallback(() => {
    api
      .get<TokenListData>('/api/tokens')
      .then(setData)
      .catch(() => {})
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const create = async () => {
    const name = newName.trim()
    if (!name) {
      setMsg('请填写 Token 名称')
      return
    }
    const rpm = parseInt(newRpm, 10)
    setBusy(true)
    setMsg('')
    try {
      const body: Record<string, unknown> = { name, rate_limit_rpm: Number.isNaN(rpm) ? 0 : rpm }
      if (showCustom && customToken.trim()) body.token = customToken.trim()
      const daysStr = expiryPreset === 'custom' ? expiryCustom : expiryPreset
      const days = parseInt(daysStr, 10)
      if (!Number.isNaN(days) && days > 0) body.expires_in_days = days
      const c = await api.post<CreatedToken>('/api/tokens', body)
      setCreated(c)
      setNewName('')
      setNewRpm('')
      setCustomToken('')
      setShowCustom(false)
      setExpiryPreset('')
      setExpiryCustom('')
      load()
    } catch (e) {
      setMsg(e instanceof Error ? e.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  const copyToken = async () => {
    if (!created) return
    try {
      await navigator.clipboard.writeText(created.token)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      /* 剪贴板不可用时手动复制 */
    }
  }

  const saveEdit = async (id: number) => {
    const edit = editing[id]
    if (!edit) return
    const name = edit.name.trim()
    const rpm = parseInt(edit.rpm, 10)
    setBusy(true)
    try {
      await api.put(`/api/tokens/${id}`, {
        name,
        ...(Number.isNaN(rpm) ? {} : { rate_limit_rpm: rpm }),
      })
      setEditing((s) => {
        const n = { ...s }
        delete n[id]
        return n
      })
      load()
    } catch (e) {
      setMsg(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const revoke = async () => {
    if (!revokeTarget) return
    setBusy(true)
    setMsg('')
    try {
      await api.delete(`/api/tokens/${revokeTarget.id}`)
      setRevokeTarget(null)
      load()
    } catch (e) {
      setMsg(e instanceof Error ? e.message : '删除失败')
      setRevokeTarget(null)
    } finally {
      setBusy(false)
    }
  }

  const defaultRpm = data?.default_rpm ?? 0
  const tokens = data?.tokens ?? []

  return (
    <div className="space-y-4">
      {/* 列表 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="h-4 w-4" /> 代理 Token
          </CardTitle>
          <CardDescription>
            供后端服务调用 /proxy/* 的 Bearer Token（数据库哈希存储，明文仅创建时展示一次）·
            全局默认限流：{defaultRpm > 0 ? `${defaultRpm} 次/分钟` : '未启用（config.yaml rate_limit.default_rpm 配置）'}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            {tokens.map((t) => {
              const edit = editing[t.id]
              return (
                <div key={t.id} className="flex flex-wrap items-center gap-2 rounded-md border p-2.5">
                  <span
                    className={`h-2.5 w-2.5 shrink-0 rounded-full ${
                      t.expires_at && isExpired(t.expires_at)
                        ? 'bg-red-500'
                        : t.expires_at && isExpiringSoon(t.expires_at)
                          ? 'bg-amber-500'
                          : 'bg-emerald-500'
                    }`}
                    title={
                      t.expires_at && isExpired(t.expires_at)
                        ? '已作废（已过期）'
                        : t.expires_at && isExpiringSoon(t.expires_at)
                          ? '即将过期'
                          : '正常可用'
                    }
                  />
                  {edit ? (
                    <>
                      <Input
                        value={edit.name}
                        onChange={(e) => setEditing((s) => ({ ...s, [t.id]: { ...edit, name: e.target.value } }))}
                        className="w-44"
                        placeholder="名称"
                      />
                      <Input
                        value={edit.rpm}
                        onChange={(e) => setEditing((s) => ({ ...s, [t.id]: { ...edit, rpm: e.target.value } }))}
                        className="w-32"
                        placeholder="次/分钟"
                        type="number"
                        min={0}
                        title="0 = 跟随全局默认"
                      />
                      <Button size="sm" onClick={() => saveEdit(t.id)} disabled={busy}>
                        保存
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() =>
                          setEditing((s) => {
                            const n = { ...s }
                            delete n[t.id]
                            return n
                          })
                        }
                      >
                        取消
                      </Button>
                    </>
                  ) : (
                    <>
                      <span className="min-w-28 font-medium">{t.name}</span>
                      <Badge variant="secondary" className="font-mono">ID {t.id}</Badge>
                      {t.expires_at && (
                        <Badge variant={isExpired(t.expires_at) ? 'destructive' : isExpiringSoon(t.expires_at) ? 'secondary' : 'outline'} className="text-xs">
                          {isExpired(t.expires_at) ? '已过期' : `过期 ${fmtTime(t.expires_at)}`}
                        </Badge>
                      )}
                      <span className="text-xs text-muted-foreground">
                        {t.rate_limit_rpm > 0 ? `独立限流 ${t.rate_limit_rpm} 次/分` : `默认限流${defaultRpm > 0 ? ` ${defaultRpm} 次/分` : '（未启用）'}`}
                      </span>
                      <span className="text-xs text-muted-foreground">创建于 {fmtTime(t.created_at)}</span>
                      <span className="text-xs text-muted-foreground">
                        {t.last_used_at ? `最后使用 ${fmtTime(t.last_used_at)}` : '从未使用'}
                      </span>
                      <div className="flex-1" />
                      <Button
                        size="icon"
                        variant="ghost"
                        title="编辑名称/限流"
                        onClick={() =>
                          setEditing((s) => ({
                            ...s,
                            [t.id]: { name: t.name, rpm: String(t.rate_limit_rpm) },
                          }))
                        }
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button size="icon" variant="ghost" title="吊销" onClick={() => setRevokeTarget({ id: t.id, name: t.name })}>
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </>
                  )}
                </div>
              )
            })}
            {tokens.length === 0 && <p className="text-sm text-muted-foreground">暂无 Token</p>}
          </div>

          {/* 新建 */}
          <div className="space-y-2 border-t pt-4">
            <div className="flex flex-wrap gap-2">
              <Input
                placeholder="Token 名称（如：订单服务）"
                value={newName}
                onChange={(e) => {
                  setNewName(e.target.value)
                  if (msg) setMsg('')
                }}
                onKeyDown={(e) => e.key === 'Enter' && create()}
                className="w-56"
              />
              <Input
                placeholder="独立限流（次/分钟，0=默认）"
                value={newRpm}
                onChange={(e) => setNewRpm(e.target.value)}
                type="number"
                min={0}
                className="w-60"
              />
              <Button onClick={create} disabled={busy}>
                <Plus className="h-4 w-4" /> 生成 Token
              </Button>
            </div>
            <button
              className="text-xs text-muted-foreground underline-offset-2 hover:underline"
              onClick={() => setShowCustom((v) => !v)}
            >
              {showCustom ? '收起自定义值' : '使用自定义 Token 值（默认自动生成 sk- 随机串）'}
            </button>
            {showCustom && (
              <Input
                placeholder="自定义 Token 值（16~128 字符，仅本次可见）"
                value={customToken}
                onChange={(e) => setCustomToken(e.target.value)}
                className="font-mono text-xs"
              />
            )}
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span>过期时间：</span>
              <select
                value={expiryPreset}
                onChange={(e) => setExpiryPreset(e.target.value)}
                className="rounded-md border bg-card px-2 py-1 text-xs"
              >
                <option value="">永不过期</option>
                <option value="7">7 天</option>
                <option value="30">30 天</option>
                <option value="90">90 天</option>
                <option value="custom">自定义天数…</option>
              </select>
              {expiryPreset === 'custom' && (
                <Input
                  type="number"
                  min={1}
                  placeholder="天数"
                  value={expiryCustom}
                  onChange={(e) => setExpiryCustom(e.target.value)}
                  className="w-20 text-xs"
                  autoFocus
                />
              )}
            </div>
          </div>
          {msg && <p className="text-sm text-destructive">{msg}</p>}
        </CardContent>
      </Card>

      {/* 使用说明 */}
      <Card>
        <CardHeader>
          <CardTitle>调用方式</CardTitle>
        </CardHeader>
        <CardContent>
          <pre className="overflow-x-auto rounded-md bg-muted p-3 text-xs">curl -H "Authorization: Bearer &lt;你的Token&gt;" "http://localhost:8080/proxy/any-path"</pre>
          <p className="mt-2 text-xs text-muted-foreground">
            超出限流额度时返回 429 与 Retry-After 头；吊销立即生效。所有增删改操作均记入审计日志。
          </p>
        </CardContent>
      </Card>

      {/* 创建成功弹窗：明文仅展示一次 */}
      <Dialog open={created !== null} onClose={() => setCreated(null)} title="Token 创建成功">
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            「{created?.name}」的明文值<strong>仅此一次</strong>展示，请立即复制保存（数据库只存哈希，离开本弹窗后无法找回）：
          </p>
          <div className="flex items-center gap-2">
            <code className="flex-1 break-all rounded-md bg-muted p-2.5 font-mono text-xs">{created?.token}</code>
            <Button size="sm" variant="outline" onClick={copyToken}>
              <Copy className="h-4 w-4" /> {copied ? '已复制' : '复制'}
            </Button>
          </div>
          <div className="flex justify-end">
            <Button onClick={() => setCreated(null)}>
              <RefreshCw className="mr-1 h-3.5 w-3.5" /> 我已保存
            </Button>
          </div>
        </div>
      </Dialog>

      {/* 吊销确认弹窗 */}
      <ConfirmDialog
        open={!!revokeTarget}
        title="吊销 Token"
        message={`确定吊销 Token「${revokeTarget?.name}」？使用它的服务将立即收到 401。`}
        confirmText="吊销"
        onConfirm={revoke}
        onClose={() => setRevokeTarget(null)}
      />
    </div>
  )
}

function fmtTime(s: string): string {
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}

function isExpired(s: string): boolean {
  return new Date(s).getTime() < Date.now()
}

function isExpiringSoon(s: string): boolean {
  const ms = new Date(s).getTime() - Date.now()
  return ms > 0 && ms < 7 * 24 * 3600 * 1000 // 7 天内
}
