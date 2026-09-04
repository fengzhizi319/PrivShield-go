/**
 * Tailwind CSS 配置文件
 * Tailwind CSS configuration file
 *
 * Tailwind 是一个实用优先 (utility-first) 的 CSS 框架，
 * Tailwind is a utility-first CSS framework,
 * 本配置定义了哪些文件中的 class 需要被扫描和生成。
 * This config defines which files' classes should be scanned and generated.
 *
 * 在本项目中，所有 React 组件使用 Tailwind 工具类进行样式编排，
 * In this project, all React components use Tailwind utility classes for styling,
 * 无额外自定义主题扩展或插件。
 * with no additional custom theme extensions or plugins.
 *
 * @type {import('tailwindcss').Config}
 */
export default {
  // 内容扫描路径：Tailwind 会扫描这些文件中的 class 名，仅生成使用到的 CSS
  // Content scan paths: Tailwind scans these files for class names, only generating used CSS
  content: [
    "./index.html",                  // HTML 入口 / HTML entry point
    "./src/**/*.{js,ts,jsx,tsx}",    // 所有源代码文件 / All source code files
  ],
  // 主题扩展（本项目未使用自定义主题）
  // Theme extension (this project uses no custom theme)
  theme: {
    extend: {},  // 空扩展，使用 Tailwind 默认主题 / Empty extension, uses Tailwind default theme
  },
  // 插件列表（本项目未使用额外插件）
  // Plugin list (this project uses no additional plugins)
  plugins: [],
}
