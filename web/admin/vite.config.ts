import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 开发期所有请求经 Vite 代理转发到网关(8080), 前端不直连业务服务.
// /ws 同时代理 WebSocket(alarm / dashboard 两个通道).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      // ⚠️ 演示专用: 直连可启动的 M2 服务(网关 M6 不在本机). 联调前改回仅 '/api' -> 8080.
      '/api/visitors': { target: 'http://localhost:8083', changeOrigin: true },
      '/api/visitor': { target: 'http://localhost:8083', changeOrigin: true },
      '/api/notices': { target: 'http://localhost:8085', changeOrigin: true },
      '/api/notice': { target: 'http://localhost:8085', changeOrigin: true },
      '/api/parking': { target: 'http://localhost:8084', changeOrigin: true },
      '/api/monthly': { target: 'http://localhost:8084', changeOrigin: true },
      '/api/workorder': { target: 'http://localhost:8082', changeOrigin: true },
      '/api/dashboard': { target: 'http://localhost:8052', changeOrigin: true },
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws': { target: 'http://localhost:8080', ws: true, changeOrigin: true },
    },
  },
})
