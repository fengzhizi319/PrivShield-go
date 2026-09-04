/**
 * 响应查看器组件：展示请求结果 / Response Viewer Component: Display Request Results
 *
 * 三种状态 / Three States：
 *   - 空状态（未发送请求）/ Empty state (no request sent)
 *   - 错误状态（网络失败 / 代理错误）/ Error state (network failure / proxy error)
 *   - 成功状态（JSON 语法高亮 + 复制/下载）/ Success state (JSON syntax highlight + copy/download)
 *
 * 详细逻辑 / Detailed Logic：
 *   1. highlightJson 用正则将 JSON 按 token 着色（键/字符串/数字/布尔/null）；
 *   2. truncateLongStrings 截断超长 base64/dataURI 避免填满屏幕；
 *   3. CopyButton / DownloadButton 提供快捷操作；
 *   4. 成功状态显示 HTTP 状态码、后端身份(via)、协议(protocol)、耗时。
 */

/** 引入 React Hooks 和类型 / Import React Hooks and types */
import { useState, type ReactNode } from 'react';
/** 引入代理响应类型 / Import proxy response type */
import type { ProxyResponse } from '@/types/api';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';

/**
 * ResponsePanel 组件属性接口 / ResponsePanel Component Props Interface
 */
interface ResponsePanelProps {
  /** 代理响应数据（null 表示未发送）/ Proxy response data (null = not sent) */
  response: ProxyResponse | null;
  /** 错误信息（null 表示无错误）/ Error message (null = no error) */
  error: string | null;
  /** 前端测量的请求耗时（毫秒）/ Frontend-measured request duration (ms) */
  duration: number | null;
  /** 当前请求路径，用于下载文件命名 / Current request path, used for download file naming */
  path?: string;
}

/**
 * 轻量 JSON 语法高亮 / Lightweight JSON Syntax Highlighting
 *
 * 将序列化后的 JSON 按 token 着色，不引入第三方库。
 * Colors serialized JSON by token without third-party libraries.
 *
 * 配色方案 / Color Scheme：
 *   - 键名（"key":) → 天蓝色 / Key names → sky blue
 *   - 字符串值 → 翠绿色 / String values → emerald green
 *   - 数字 → 橙色 / Numbers → orange
 *   - 布尔值 → 琥珀色 / Booleans → amber
 *   - null → 玫瑰红 / null → rose red
 *
 * @param json - 格式化后的 JSON 字符串 / Formatted JSON string
 * @returns React 节点数组（带颜色的 span）/ Array of React nodes (colored spans)
 */
function highlightJson(json: string): ReactNode[] {
  // 防御性安全限制：超过 100k 字符时停止正则高亮，改用纯文本渲染，防止 ReDoS
  // Defensive safety limit: stop regex highlighting beyond 100k chars, use plain text to prevent ReDoS
  if (json.length > 100_000) {
    return [<span key={0}>{json}</span>]; // 纯文本回退 / Plain text fallback
  }
  // 正则匹配 JSON token：字符串(\"...\") / 布尔 / null / 数字
  // Regex matches JSON tokens: string("...") / boolean / null / number
  const tokenRegex =
    /("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+-]?\d+)?)/g;
  const nodes: ReactNode[] = []; // 结果节点数组 / Result nodes array
  let last = 0;                  // 上次匹配结束位置 / Last match end position
  let match: RegExpExecArray | null; // 当前匹配结果 / Current match result
  let key = 0;                   // React key 计数器 / React key counter

  // 遍历所有匹配的 token / Iterate all matched tokens
  while ((match = tokenRegex.exec(json)) !== null) {
    // 匹配前的空白/标点部分（不着色）/ Whitespace/punctuation before match (no color)
    if (match.index > last) {
      nodes.push(<span key={key++}>{json.slice(last, match.index)}</span>);
    }
    const token = match[0]; // 当前 token 文本 / Current token text
    let cls = 'text-orange-600'; // 默认数字颜色 / Default number color
    if (/^"/.test(token)) {
      // 以冒号结尾 → 键名（天蓝）；否则 → 字符串值（翠绿）
      // Ends with colon → key name (sky blue); otherwise → string value (emerald)
      cls = /:$/.test(token) ? 'text-sky-700' : 'text-emerald-700';
    } else if (/true|false/.test(token)) {
      cls = 'text-amber-600'; // 布尔值（琥珀）/ Boolean (amber)
    } else if (/null/.test(token)) {
      cls = 'text-rose-500';  // null（玫瑰红）/ null (rose)
    }
    // 推入带颜色的 token 节点 / Push colored token node
    nodes.push(
      <span key={key++} className={cls}>
        {token}
      </span>,
    );
    last = match.index + token.length; // 更新位置 / Update position
  }
  // 处理末尾剩余文本 / Handle remaining text at end
  if (last < json.length) {
    nodes.push(<span key={key++}>{json.slice(last)}</span>);
  }
  return nodes;
}

/**
 * 截断响应中超长的 base64 / data URI 字符串 / Truncate overly long base64 / data URI strings in response
 *
 * 避免图片编码内容填满屏幕，仅展示分级结果等有效信息。
 * Prevents image-encoded content from filling the screen, shows only classification results.
 *
 * 规则 / Rules：
 *   - data:image/ 开头的 data URI → "[image data, ~N KB]"
 *   - 纯 base64（无空格、符合 A-Za-z0-9+/=、长度>200）→ "[base64 data, ~N KB]"
 *   - 其他超长字符串（>500）→ 截断前 80 字符 + "…(N chars)"
 *   - 限制最大递归深度（depth<=20）防止栈溢出 / Limit max recursion depth (depth<=20) to prevent stack overflow
 *
 * @param obj - 待处理的任意值 / Any value to process
 * @param depth - 当前递归深度 / Current recursion depth
 * @returns 截断后的值 / Truncated value
 */
function truncateLongStrings(obj: unknown, depth = 0): unknown {
  if (depth > 20) return obj; // 超过最大深度，直接返回 / Exceed max depth, return as-is
  if (typeof obj === 'string') {
    // 检测 data URI 图片（如 data:image/png;base64,...) / Detect data URI images
    const dataUriMatch = obj.match(/^data:image\/[a-zA-Z]+;base64,/);
    if (dataUriMatch) {
      const rawLen = obj.length - dataUriMatch[0].length; // 纯 base64 部分长度 / Pure base64 part length
      const kb = Math.max(1, Math.round((rawLen * 3) / 4 / 1024)); // 估算原始字节数 / Estimate original byte count
      return `[image data, ~${kb} KB]`; // 替换为摘要 / Replace with summary
    }
    // 纯 base64（不含空格，连续字符长度>200 且符合标准 Base64 字符集）
    // Pure base64 (no whitespace, length>200, matches standard Base64 charset)
    if (obj.length > 200 && !/\s/.test(obj) && /^[A-Za-z0-9+/=]+$/.test(obj.slice(0, 128))) {
      const kb = Math.max(1, Math.round((obj.length * 3) / 4 / 1024)); // 估算 KB / Estimate KB
      return `[base64 data, ~${kb} KB]`; // 替换为摘要 / Replace with summary
    }
    // 其他超长字符串：截断前 80 字符 + 总长度提示 / Other long strings: truncate first 80 chars + total length hint
    if (obj.length > 500) {
      return obj.slice(0, 80) + `…(${obj.length} chars)`;
    }
    return obj; // 正常长度字符串直接返回 / Normal length string returned as-is
  }
  // 数组：递归处理每个元素 / Array: recursively process each element
  if (Array.isArray(obj)) {
    return obj.map((item) => truncateLongStrings(item, depth + 1));
  }
  // 对象：递归处理每个属性值 / Object: recursively process each property value
  if (obj !== null && typeof obj === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(obj)) {
      out[k] = truncateLongStrings(v, depth + 1); // 深度+1 / Depth +1
    }
    return out;
  }
  return obj; // 基本类型直接返回 / Primitive types returned as-is
}

/**
 * 复制到剪贴板按钮 / Copy to Clipboard Button
 *
 * 复制成功后短暂显示对勾反馈（1.5s）。
 * Shows brief checkmark feedback (1.5s) after successful copy.
 */
function CopyButton({ text }: { text: string }) {
  const { t } = useI18n();       // 获取翻译函数 / Get translation function
  const [copied, setCopied] = useState(false); // 复制成功标记 / Copy success flag
  /** 点击复制处理 / Click copy handler */
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text); // 写入剪贴板 / Write to clipboard
      setCopied(true);                           // 显示成功 / Show success
      setTimeout(() => setCopied(false), 1500);  // 1.5s 后恢复 / Restore after 1.5s
    } catch {
      /* 忽略剪贴板不可用 / Ignore clipboard unavailable */
    }
  };
  return (
    <button
      onClick={handleCopy}
      className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700"
      title={t('response.copy')}
    >
      {/* 复制成功时显示对勾图标 / Show checkmark icon when copied */}
      <Icon name={copied ? 'check' : 'copy'} className="h-3.5 w-3.5" />
      {copied ? t('response.copied') : t('response.copy')}
    </button>
  );
}

/**
 * 下载响应 JSON 为文件按钮 / Download Response JSON as File Button
 *
 * 文件名由请求路径派生（非法字符替换为下划线）。
 * Filename derived from request path (illegal chars replaced with underscores).
 */
function DownloadButton({ text, path }: { text: string; path: string }) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function
  /** 点击下载处理 / Click download handler */
  const handleDownload = () => {
    const blob = new Blob([text], { type: 'application/json' }); // 创建 JSON Blob / Create JSON Blob
    const url = URL.createObjectURL(blob);   // 生成临时 URL / Generate temporary URL
    const a = document.createElement('a');   // 创建临时链接元素 / Create temporary anchor element
    // 路径转安全文件名：非法字符替换为 _，去除首尾 _ / Path to safe filename: replace illegal chars with _, trim leading/trailing _
    const safeName = path.replace(/[^a-zA-Z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'response';
    a.href = url;              // 设置下载地址 / Set download URL
    a.download = `${safeName}.json`; // 设置文件名 / Set filename
    a.click();                 // 触发下载 / Trigger download
    URL.revokeObjectURL(url);  // 释放临时 URL / Release temporary URL
  };
  return (
    <button
      onClick={handleDownload}
      className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700"
      title={t('response.download')}
    >
      <Icon name="download" className="h-3.5 w-3.5" />
      {t('response.download')}
    </button>
  );
}

/**
 * 响应面板主组件 / Response Panel Main Component
 *
 * 根据 response/error 状态渲染三种视图：
 * Renders three views based on response/error state:
 *   1. 空状态：未发送请求时显示闪电图标提示 / Empty: lightning icon hint when no request sent
 *   2. 错误状态：红色头部 + 错误详情 / Error: red header + error details
 *   3. 成功状态：状态码 + via/protocol 徽章 + 耗时 + 高亮 JSON / Success: status + via/protocol badges + duration + highlighted JSON
 */
export default function ResponsePanel({ response, error, duration, path = 'response' }: ResponsePanelProps) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function

  /* ====== 空状态：未发送请求 / Empty State: No Request Sent ====== */
  if (!response && !error) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-gray-300">
        <Icon name="zap" className="h-10 w-10" strokeWidth={1.5} />
        <p className="text-sm text-gray-400">{t('response.empty')}</p>
      </div>
    );
  }

  /* ====== 错误状态：网络失败或代理错误 / Error State: Network Failure or Proxy Error ====== */
  if (error) {
    return (
      <div className="flex h-full flex-col">
        {/* 错误头部：红色警告图标 + 耗时 / Error header: red alert icon + duration */}
        <div className="flex items-center justify-between border-b border-red-100 px-4 py-2.5">
          <span className="inline-flex items-center gap-2 text-sm font-semibold text-red-600">
            <Icon name="alert" className="h-4 w-4" />
            {t('response.failed')}
          </span>
          {/* 显示失败耗时（若有）/ Show failure duration (if available) */}
          {duration !== null && (
            <span className="text-xs text-gray-400">{duration.toFixed(1)} ms</span>
          )}
        </div>
        {/* 错误详情（等宽字体、红色、可滚动）/ Error details (monospace, red, scrollable) */}
        <pre className="flex-1 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs leading-relaxed text-red-600">
          {error}
        </pre>
      </div>
    );
  }

  /* ====== 成功状态 / Success State ====== */
  // 对响应 data 中的超长 base64/dataURI 做截断处理 / Truncate long base64/dataURI in response data
  const jsonText = JSON.stringify(truncateLongStrings(response?.data), null, 2);
  // 后端身份标识：via 为处理请求的控制台后端，protocol 为其与 agent 的通信协议
  // Backend identity: via = console backend that processed request, protocol = communication protocol with agent
  const via = response?.via;
  const protocol = response?.protocol;
  return (
    <div className="flex h-full flex-col">
      {/* 成功头部：状态码 + via + protocol + 耗时 + 操作按钮 / Success header: status + via + protocol + duration + actions */}
      <div className="flex items-center justify-between border-b border-gray-100 px-4 py-2.5">
        <div className="flex flex-wrap items-center gap-2">
          {/* HTTP 状态码徽章（绿色圆点 + 数字）/ HTTP status badge (green dot + number) */}
          <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-50 px-2 py-0.5 text-xs font-semibold text-emerald-700">
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
            {response?.status ?? 200}
          </span>
          {/* 后端身份徽章（如 "go-grpc") / Backend identity badge (e.g. "go-grpc") */}
          {via && (
            <span
              className="inline-flex items-center rounded-full bg-indigo-50 px-2 py-0.5 text-xs font-semibold text-indigo-700"
              title={t('response.via_title')}
            >
              {via}
            </span>
          )}
          {/* 通信协议徽章（如 "REST" / "gRPC")/ Protocol badge (e.g. "REST" / "gRPC") */}
          {protocol && (
            <span
              className="inline-flex items-center rounded-full bg-sky-50 px-2 py-0.5 text-xs font-semibold text-sky-700"
              title={t('response.protocol_title')}
            >
              {protocol}
            </span>
          )}
          {/* 耗时显示：优先用后端返回的 duration_ms，否则用前端测量值 / Duration: prefer backend duration_ms, else frontend measurement */}
          <span className="text-xs text-gray-400">
            {response?.duration_ms !== undefined
              ? `${response.duration_ms.toFixed(2)} ms`
              : duration !== null
                ? `${duration.toFixed(1)} ms`
                : ''}
          </span>
        </div>
        {/* 操作按钮组：复制 + 下载 / Action buttons: copy + download */}
        <div className="flex items-center">
          <CopyButton text={jsonText} />
          <DownloadButton text={jsonText} path={path} />
        </div>
      </div>
      {/* JSON 响应体（语法高亮、可滚动）/ JSON response body (syntax highlighted, scrollable) */}
      <pre className="flex-1 overflow-auto whitespace-pre-wrap break-words bg-gray-50/50 p-4 font-mono text-xs leading-relaxed text-gray-700">
        {highlightJson(jsonText)}
      </pre>
    </div>
  );
}
