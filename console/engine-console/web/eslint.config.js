/**
 * ESLint 代码检查配置文件（Flat Config 格式）
 * ESLint linting configuration file (Flat Config format)
 *
 * ESLint 9+ 使用 flat config 格式（eslint.config.js）替代旧的 .eslintrc。
 * ESLint 9+ uses flat config format (eslint.config.js) replacing the old .eslintrc.
 *
 * 本配置包含：
 * This configuration includes:
 *   - TypeScript ESLint 推荐规则 / TypeScript ESLint recommended rules
 *   - React Hooks 规则（确保 Hooks 调用顺序正确）/ React Hooks rules (ensure correct hook call order)
 *   - React Refresh 规则（确保组件可热更新）/ React Refresh rules (ensure components are hot-reloadable)
 *   - 自定义规则：any 警告、未使用变量警告 / Custom rules: any warning, unused vars warning
 */
import js from '@eslint/js';                         // ESLint 核心 JS 规则 / ESLint core JS rules
import globals from 'globals';                       // 全局变量定义 / Global variable definitions
import reactHooks from 'eslint-plugin-react-hooks';   // React Hooks 检查插件 / React Hooks lint plugin
import reactRefresh from 'eslint-plugin-react-refresh'; // React Refresh 检查插件 / React Refresh lint plugin
import tseslint from 'typescript-eslint';             // TypeScript ESLint 集成 / TypeScript ESLint integration

export default tseslint.config(
  // 忽略构建输出目录 / Ignore build output directory
  { ignores: ['dist'] },
  {
    // 继承推荐规则集（JS + TypeScript）/ Extend recommended rule sets (JS + TypeScript)
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    // 仅对 TypeScript 文件生效 / Only applies to TypeScript files
    files: ['**/*.{ts,tsx}'],
    // 语言选项 / Language options
    languageOptions: {
      ecmaVersion: 2020,        // 目标 ECMAScript 版本 / Target ECMAScript version
      globals: globals.browser, // 浏览器全局变量 (window, document 等) / Browser globals (window, document, etc.)
    },
    // 插件注册 / Plugin registration
    plugins: {
      'react-hooks': reactHooks,     // React Hooks 规则 / React Hooks rules
      'react-refresh': reactRefresh, // React Refresh 规则 / React Refresh rules
    },
    // 自定义规则配置 / Custom rule configuration
    rules: {
      // React Hooks 推荐规则（禁止条件调用、确保依赖数组完整）
      // React Hooks recommended rules (no conditional calls, ensure complete dependency arrays)
      ...reactHooks.configs.recommended.rules,
      // React Refresh: 仅导出组件（警告混合导出，允许常量导出）
      // React Refresh: only export components (warn on mixed exports, allow constant exports)
      'react-refresh/only-export-components': [
        'warn',
        { allowConstantExport: true },
      ],
      // 禁止显式 any（警告级别，不阻断编译）
      // Disallow explicit any (warning level, doesn't block compilation)
      '@typescript-eslint/no-explicit-any': 'warn',
      // 未使用变量警告（忽略 _ 前缀的参数）
      // Unused vars warning (ignore parameters with _ prefix)
      '@typescript-eslint/no-unused-vars': ['warn', { argsIgnorePattern: '^_' }],
    },
  },
);
