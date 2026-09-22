import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 开发期所有请求经 Vite 代理转发到网关(8080), 前端不直连业务服务.
// /ws 同时代理 WebSocket(alarm / dashboard 两个通道).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws': { target: 'http://localhost:8080', ws: true, changeOrigin: true },
    },
  },
})
