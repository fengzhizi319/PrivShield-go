/**
 * PostCSS 配置文件
 * PostCSS configuration file
 *
 * PostCSS 是一个 CSS 转换管道工具，本配置注册了两个插件：
 * PostCSS is a CSS transformation pipeline tool. This config registers two plugins:
 *   1. tailwindcss - 将 Tailwind 工具类编译为实际 CSS
 *      Compiles Tailwind utility classes into actual CSS
 *   2. autoprefixer - 自动添加浏览器厂商前缀（如 -webkit-, -moz-）
 *      Automatically adds browser vendor prefixes (e.g. -webkit-, -moz-)
 *
 * Vite 在构建时自动加载此配置，无需手动调用。
 * Vite automatically loads this config during build, no manual invocation needed.
 */
export default {
  plugins: {
    tailwindcss: {},   // Tailwind CSS 编译插件 / Tailwind CSS compilation plugin
    autoprefixer: {},  // 浏览器前缀自动补全 / Browser prefix auto-completion
  },
}
