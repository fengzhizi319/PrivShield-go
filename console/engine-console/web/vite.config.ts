/// <reference types="vitest" />
/**
 * Vite 构建工具配置文件
 * Vite build tool configuration file
 *
 * 本文件定义了前端项目的构建、开发服务器和测试配置。
 * This file defines the build, dev server, and test configuration for the frontend project.
 *
 * 主要配置项 / Key configurations:
 *   - plugins: React 快速刷新插件 / React Fast Refresh plugin
 *   - resolve.alias: '@' 路径别名指向 src/ / '@' path alias points to src/
 *   - server.proxy: 开发模式下 /v1 与 /health 请求代理到 Go BFF 后端 8081 / Dev mode proxies /v1 and /health to Go BFF backend 8081
 *   - build: 输出到 dist/ 目录 / Output to dist/ directory
 *   - test: Vitest 单元测试配置 (jsdom 环境) / Vitest unit test config (jsdom environment)
 */
import { defineConfig } from 'vite'       // Vite 核心配置函数 / Vite core config function
import react from '@vitejs/plugin-react'   // React 快速刷新插件 / React Fast Refresh plugin
import path from 'path'                    // Node.js 路径工具 / Node.js path utility

// https://vitejs.dev/config/
export default defineConfig({
  // 插件列表：启用 React JSX 转换和热更新
  // Plugin list: enable React JSX transform and hot reload
  plugins: [react()],
  // 模块解析配置 / Module resolution configuration
  resolve: {
    alias: {
      // '@' 别名映射到 src 目录，简化导入路径（如 '@/components/Header'）
      // '@' alias maps to src directory, simplifies import paths (e.g. '@/components/Header')
      '@': path.resolve(__dirname, './src'),
    },
  },
  // 开发服务器配置 / Dev server configuration
  server: {
    port: 5173,  // 开发服务器端口 / Dev server port
    proxy: {
      // 开发模式下将 /v1 前缀的请求代理到 Go 控制台 BFF
      // In dev mode, proxy /v1-prefixed requests to the Go console BFF
      '/v1': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:8081',  // 代理目标（优先环境变量，默认 Go 后端 8081）
        changeOrigin: true,               // 修改 Origin 头以匹配目标 / Modify Origin header to match target
      },
      // BFF 自身健康检查（无前缀端点）
      // Health check endpoint (unprefixed)
      '/health': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:8081',
        changeOrigin: true,
      },
    },
  },
  // 生产构建配置 / Production build configuration
  build: {
    outDir: 'dist',       // 输出目录 / Output directory
    emptyOutDir: true,    // 构建前清空输出目录 / Empty output dir before build
  },
  // Vitest 单元测试配置 / Vitest unit test configuration
  test: {
    globals: true,                    // 启用全局 API（无需导入 describe/it/expect）/ Enable global APIs (no need to import describe/it/expect)
    environment: 'jsdom',             // 使用 jsdom 模拟浏览器环境 / Use jsdom to simulate browser environment
    setupFiles: './src/test/setup.ts', // 测试初始化文件（注册 jest-dom 匹配器）/ Test setup file (registers jest-dom matchers)
  },
})
