/**
 * cURL 命令生成模块 / cURL Command Generator Module
 *
 * 功能：将当前请求导出为可直接在终端执行的 cURL 命令，
 * 目标为 PrivShield 的 REST API（默认 http://127.0.0.1:8079）。
 *
 * Function: Export the current request as a ready-to-run cURL command,
 * targeting the PrivShield REST API (default http://127.0.0.1:8079).
 *
 * 使用场景 / Usage Scenarios：
 *   - EndpointView 中点击 "cURL" 按钮复制命令到剪贴板；
 *   - 便于开发者在终端直接调试 agent 接口而无需经过控制台代理。
 */

/**
 * 对 shell 单引号转义，保证生成的 cURL 可安全粘贴执行。
 * Escape shell single quotes to ensure the generated cURL can be safely pasted and executed.
 *
 * 逻辑：将字符串包裹在单引号中，内部的单引号替换为 '\''（结束引号 + 转义引号 + 重新开始引号）。
 * Logic: Wrap the string in single quotes; replace internal single quotes with '\'' (end quote + escaped quote + restart quote).
 *
 * @param value - 需要转义的原始字符串 / The raw string to escape
 * @returns 安全的 shell 单引号字符串 / Shell-safe single-quoted string
 */
function shellQuote(value: string): string {
  // 用单引号包裹，并将内部的 ' 替换为 '\''（POSIX shell 标准转义方式）
  // Wrap in single quotes and replace internal ' with '\'' (POSIX shell standard escaping)
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

/**
 * cURL 生成选项接口 / cURL Generation Options Interface
 *
 * 定义生成一条 cURL 命令所需的全部参数。
 * Defines all parameters needed to generate a single cURL command.
 */
export interface CurlOptions {
  /** HTTP 方法（GET / POST 等）/ HTTP method (GET / POST etc.) */
  method: string;
  /** 请求路径（如 /v1/privacy/mask）/ Request path (e.g. /v1/privacy/mask) */
  path: string;
  /** 请求体 JSON 文本（GET 或空则忽略）/ Request body JSON text (ignored for GET or empty) */
  body?: string;
  /** agent REST 基础地址，默认 http://127.0.0.1:8079 / Agent REST base URL, default http://127.0.0.1:8079 */
  baseUrl?: string;
}

/**
 * 构建完整的 cURL 命令字符串 / Build a complete cURL command string
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 去除 baseUrl 尾部斜杠，拼接完整 URL；
 *   2. 非 GET 方法添加 -X 参数；
 *   3. URL 始终用单引号包裹（防止 shell 解析特殊字符）；
 *   4. 非 GET 且请求体非空/非 {} 时，添加 Content-Type 头与 -d 数据参数。
 *
 * @param options - cURL 生成选项 / cURL generation options
 * @returns 可直接执行的 cURL 命令字符串 / Ready-to-execute cURL command string
 */
export function buildCurl({ method, path, body, baseUrl = 'http://127.0.0.1:8079' }: CurlOptions): string {
  // 去除基址尾部斜杠，避免拼接出双斜杠（如 http://host//path）
  // Remove trailing slash from base URL to avoid double slashes (e.g. http://host//path)
  const base = baseUrl.replace(/\/$/, '');
  // 确保 path 以 / 开头，拼接为完整 URL / Ensure path starts with / and concatenate into full URL
  const url = `${base}${path.startsWith('/') ? path : `/${path}`}`;
  // 初始化命令片段数组，第一个元素始终是 curl / Initialize command parts array, first element is always curl
  const parts: string[] = ['curl'];

  // 统一转为大写比较；GET 是 curl 默认方法，无需显式 -X / Normalize to uppercase; GET is curl's default, no need for explicit -X
  const m = method.toUpperCase();
  if (m !== 'GET') {
    // 非 GET 方法需要显式指定 -X POST / PUT / DELETE 等
    // Non-GET methods need explicit -X POST / PUT / DELETE etc.
    parts.push('-X', m);
  }

  // URL 用单引号包裹，防止 & ? 等字符被 shell 解释 / Wrap URL in single quotes to prevent shell interpreting & ? etc.
  parts.push(shellQuote(url));

  // 去除请求体首尾空白 / Trim leading/trailing whitespace from body
  const trimmed = body?.trim();
  // 仅当非 GET 且请求体有实际内容（非空、非 {}）时才附加 -H 和 -d
  // Only attach -H and -d when non-GET and body has actual content (not empty, not {})
  if (m !== 'GET' && trimmed && trimmed !== '{}') {
    // 添加 JSON Content-Type 头 / Add JSON Content-Type header
    parts.push('-H', shellQuote('Content-Type: application/json'));
    // 添加请求体数据 / Add request body data
    parts.push('-d', shellQuote(trimmed));
  }

  // 用空格拼接所有片段为最终命令 / Join all parts with spaces into the final command
  return parts.join(' ');
}

/**
 * 从健康信息推断 agent REST 基础地址 / Derive agent REST base URL from health info.
 *
 * 详细逻辑 / Detailed Logic：
 *   - agent_url 若为 REST 地址（形如 http://127.0.0.1:8079），可直接使用；
 *   - agent_url 若为 gRPC 地址（如 127.0.0.1:50051）且无 http 前缀，
 *     则回退到默认 REST 地址 http://127.0.0.1:8079。
 *
 * @param agentUrl - 健康检查返回的 agent 地址（可能为 REST 或 gRPC）/ Agent URL from health check (may be REST or gRPC)
 * @returns 可用的 REST 基础地址 / Usable REST base URL
 */
export function deriveAgentBaseUrl(agentUrl?: string): string {
  // 正则检测是否以 http:// 或 https:// 开头（不区分大小写）
  // Regex check if it starts with http:// or https:// (case-insensitive)
  if (agentUrl && /^https?:\/\//i.test(agentUrl)) {
    // 是合法的 HTTP(S) 地址，直接返回 / It's a valid HTTP(S) URL, return directly
    return agentUrl;
  }
  // gRPC 地址或空值，回退到默认 REST 地址 / gRPC address or empty, fallback to default REST URL
  return 'http://127.0.0.1:8079';
}
