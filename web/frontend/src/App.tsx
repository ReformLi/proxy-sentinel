import { Navigate, Route, Routes } from 'react-router-dom'
import { Layout } from '@/components/Layout'
import Login from '@/pages/Login'
import Dashboard from '@/pages/Dashboard'
import Logs from '@/pages/Logs'
import Flow from '@/pages/Flow'
import Tokens from '@/pages/Tokens'
import Settings from '@/pages/Settings'

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route element={<Layout />}>
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/logs" element={<Logs />} />
        <Route path="/flow" element={<Flow />} />
        <Route path="/tokens" element={<Tokens />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
      <Route path="/" element={<Navigate to="/dashboard" replace />} />
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  )
}
