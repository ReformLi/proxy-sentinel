import { Navigate, Route, Routes } from 'react-router-dom'
import { Layout } from '@/components/Layout'
import Login from '@/pages/Login'
import Dashboard from '@/pages/Dashboard'
import Logs from '@/pages/Logs'
import AuditLogs from '@/pages/AuditLogs'
import Flow from '@/pages/Flow'
import Backends from '@/pages/Backends'
import Tokens from '@/pages/Tokens'
import Users from '@/pages/Users'
import Settings from '@/pages/Settings'

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route element={<Layout />}>
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/logs" element={<Logs />} />
        <Route path="/audit-logs" element={<AuditLogs />} />
        <Route path="/flow" element={<Flow />} />
        <Route path="/backends" element={<Backends />} />
        <Route path="/tokens" element={<Tokens />} />
        <Route path="/users" element={<Users />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
      <Route path="/" element={<Navigate to="/dashboard" replace />} />
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  )
}
