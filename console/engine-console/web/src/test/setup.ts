/**
 * Vitest 测试前置脚本 / Vitest Test Setup Script
 *
 * 在 vite.config.ts 的 test.setupFiles 中引用，每个测试文件执行前自动加载。
 * Referenced in vite.config.ts test.setupFiles, auto-loaded before each test file runs.
 *
 * 功能：引入 @testing-library/jest-dom 扩展匹配器，
 * 为 Vitest 的 expect 添加 DOM 专用断言（如 toBeInTheDocument、toHaveClass、
 * toHaveTextContent 等），使组件测试断言更直观。
 *
 * Function: Import @testing-library/jest-dom extended matchers,
 * adding DOM-specific assertions to Vitest's expect (e.g. toBeInTheDocument,
 * toHaveClass, toHaveTextContent), making component test assertions more intuitive.
 */
import '@testing-library/jest-dom';
