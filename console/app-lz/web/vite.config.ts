/**
 * Vite 构建配置 — 前端控制台的开发/构建/测试配置。
 *
 * 关键配置：
 *  - 开发服务器端口: 5174
 *  - API 代理: /v1/* 与 /health → Go BFF :8085（或 VITE_PROXY_TARGET 环境变量）
 *  - 路径别名: @ → ./src
 *  - 测试环境: Vitest + jsdom
 *  - 构建输出: dist/ 目录
 */
/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5174,
    proxy: {
      // 所有 /v1/* 请求反代到 Go BFF（默认 :8085）
      '/v1': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:8085',
        changeOrigin: true,
      },
      // BFF 自身健康检查（无前缀端点）
      '/health': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:8085',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
  },
})
