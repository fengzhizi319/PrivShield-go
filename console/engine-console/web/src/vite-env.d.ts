/// <reference types="vite/client" />

/**
 * Vite 构建期环境变量类型声明 / Vite Build-time Environment Variable Type Declarations
 *
 * 本文件为 TypeScript 提供 import.meta.env 的类型提示，
 * 使得在代码中访问 VITE_ 前缀的环境变量时获得 IDE 自动补全与类型检查。
 *
 * This file provides TypeScript type hints for import.meta.env,
 * enabling IDE auto-completion and type checking when accessing VITE_ prefixed env vars.
 *
 * ``VITE_CONSOLE_API_KEY``：可选控制台 API Key（对应后端 ``CONSOLE_API_KEY``），
 * 设置后前端请求会携带 ``Authorization: Bearer`` 头；未设置则不影响本地开发。
 * Optional console API key; when set, requests carry ``Authorization: Bearer`` header.
 */

/** 扩展 Vite 的 ImportMetaEnv 接口，声明本项目自定义的环境变量 */
/** Extend Vite's ImportMetaEnv interface to declare project-specific env vars */
interface ImportMetaEnv {
  /** 可选控制台 API Key，构建时注入 / Optional console API key, injected at build time */
  readonly VITE_CONSOLE_API_KEY?: string;
}

/** 扩展 ImportMeta 接口，将 env 属性类型指向上述 ImportMetaEnv */
/** Extend ImportMeta interface to point env property type to the above ImportMetaEnv */
interface ImportMeta {
  readonly env: ImportMetaEnv;
}

/**
 * Vite 客户端类型补充：静态资源模块声明 / Vite Client Type Augmentation: Static Asset Module Declarations
 *
 * 显式声明图片资源的模块类型，使得 TypeScript 能正确解析
 * ``import logo from './logo.png'`` 等导入语句。
 * Explicitly declare image asset module types so TypeScript can correctly resolve
 * ``import logo from './logo.png'`` style import statements.
 */

/** PNG 图片模块：导入后为 URL 字符串 / PNG image module: resolves to URL string */
declare module '*.png' {
  const src: string;
  export default src;
}

/** JPG 图片模块：导入后为 URL 字符串 / JPG image module: resolves to URL string */
declare module '*.jpg' {
  const src: string;
  export default src;
}

/** JPEG 图片模块：导入后为 URL 字符串 / JPEG image module: resolves to URL string */
declare module '*.jpeg' {
  const src: string;
  export default src;
}
