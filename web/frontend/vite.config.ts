import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

// 构建产物输出到 web/dist，由 Go embed 嵌入二进制
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
  server: {
    port: 5173,
    // 开发模式代理到本地 Go 后端
    proxy: {
      '/api': 'http://localhost:8080',
      '/proxy': 'http://localhost:8080',
    },
  },
})
