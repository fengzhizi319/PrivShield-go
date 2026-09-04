/**
 * 端点测试视图：单个接口的调试页面 / Endpoint Test View: Single API Debugging Page
 *
 * 布局结构 / Layout Structure：
 *   - 上方：接口信息栏（返回按钮 / HTTP 方法 / 可编辑路径 / 分类徽章）
 *   - 下方左右分栏：请求编辑器（格式化 / cURL / 历史 / 重载示例）与响应查看器
 *   - Top: Endpoint info bar (back button / HTTP method / editable path / category badge)
 *   - Bottom split: Request editor (format / cURL / history / reload sample) and response viewer
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 维护 path/method/bodyText 等状态，切换端点时重置；
 *   2. handleSend 解析 JSON 请求体、调用 proxyRequest、记录耗时与历史；
 *   3. 支持 Cmd/Ctrl+Enter 快捷发送；
 *   4. 提供 JSON 格式化、cURL 复制、历史回填、示例重载等辅助功能。
 *   1. Maintains path/method/bodyText states, resets on endpoint switch;
 *   2. handleSend parses JSON body, calls proxyRequest, records duration & history;
 *   3. Supports Cmd/Ctrl+Enter shortcut to send;
 *   4. Provides JSON format, cURL copy, history restore, sample reload utilities.
 */

/** 引入 React Hooks：状态 / 副作用 / 引用 / Import React Hooks: state / side effect / ref */
import { useState, useEffect, useRef } from 'react';
/** 引入类型定义 / Import type definitions */
import type { EndpointSample, ProxyResponse, HistoryEntry } from '@/types/api';
/** 引入代理请求 API / Import proxy request API */
import { proxyRequest } from '@/api/client';
/** 引入分类元数据 / Import category metadata */
import { categoryMeta } from '@/lib/categories';
/** 引入 cURL 生成工具 / Import cURL generation utility */
import { buildCurl, deriveAgentBaseUrl } from '@/lib/curl';
/** 引入历史记录工具 / Import history record utilities */
import { loadHistory, addHistory, removeHistory, clearHistory } from '@/lib/history';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入响应面板组件 / Import response panel component */
import ResponsePanel from '@/components/ResponsePanel';
/** 引入历史面板组件 / Import history panel component */
import HistoryPanel from '@/components/HistoryPanel';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';
/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * EndpointView 组件属性接口 / EndpointView Component Props Interface
 *
 * 由 App 组件传入，控制端点测试视图的数据与行为。
 * Passed from App component, controls endpoint test view data and behavior.
 */
interface EndpointViewProps {
  /** 当前测试的端点示例数据 / Current endpoint sample data being tested */
  sample: EndpointSample;
  /** 返回上一级视图的回调 / Callback to return to previous view */
  onBack: () => void;
  /** agent REST 地址（用于生成 cURL），来自健康检查 / Agent REST URL (for cURL generation), from health check */
  agentUrl?: string;
}

/**
 * 将任意值格式化为缩进 2 空格的 JSON 字符串 / Format any value as JSON string with 2-space indent
 *
 * @param value - 待格式化的值 / Value to format
 * @returns 格式化后的 JSON 字符串 / Formatted JSON string
 */
function formatJson(value: unknown): string {
  return JSON.stringify(value, null, 2); // null 表示不使用 replacer，2 为缩进空格数 / null = no replacer, 2 = indent spaces
}

/**
 * 端点测试视图主组件 / Endpoint Test View Main Component
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 维护 path/method/bodyText/loading/response/error/duration/history 等状态；
 *   2. sample 变化时 useEffect 重置全部状态；
 *   3. handleSend 解析请求体、调用代理 API、记录耗时与历史；
 *   4. Cmd/Ctrl+Enter 快捷键触发发送；
 *   5. 提供格式化 / cURL 复制 / 历史回填 / 示例重载等工具栏操作。
 */
export default function EndpointView({ sample, onBack, agentUrl }: EndpointViewProps) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function
  /** 请求路径（可编辑）/ Request path (editable) */
  const [path, setPath] = useState(sample.path);
  /** HTTP 方法 / HTTP method */
  const [method, setMethod] = useState(sample.method);
  /** 请求体文本（JSON 格式）/ Request body text (JSON format) */
  const [bodyText, setBodyText] = useState(formatJson(sample.body ?? {}));
  /** 请求加载中标记 / Request loading flag */
  const [loading, setLoading] = useState(false);
  /** 响应数据 / Response data */
  const [response, setResponse] = useState<ProxyResponse | null>(null);
  /** 错误信息 / Error message */
  const [error, setError] = useState<string | null>(null);
  /** 请求耗时（毫秒）/ Request duration (milliseconds) */
  const [duration, setDuration] = useState<number | null>(null);
  /** 请求历史列表（初始化时从 localStorage 加载）/ Request history list (loaded from localStorage on init) */
  const [history, setHistory] = useState<HistoryEntry[]>(() => loadHistory());
  /** 是否显示历史面板 / Whether to show history panel */
  const [showHistory, setShowHistory] = useState(false);
  /** cURL 复制成功标记 / cURL copy success flag */
  const [curlCopied, setCurlCopied] = useState(false);

  /**
   * 端点切换时重置全部状态 / Reset all states when endpoint switches
   *
   * 依赖数组 [sample]：仅当用户选择新端点时触发。
   * Dependency array [sample]: triggers only when user selects a new endpoint.
   */
  useEffect(() => {
    setPath(sample.path);              // 重置路径 / Reset path
    setMethod(sample.method);          // 重置方法 / Reset method
    setBodyText(formatJson(sample.body ?? {})); // 重置请求体为示例 JSON / Reset body to sample JSON
    setResponse(null);                 // 清空响应 / Clear response
    setError(null);                    // 清空错误 / Clear error
    setDuration(null);                 // 清空耗时 / Clear duration
    setShowHistory(false);             // 关闭历史面板 / Close history panel
  }, [sample]);

  /**
   * 发送请求处理函数 / Send Request Handler
   *
   * 详细逻辑 / Detailed Logic：
   *   1. 设置 loading 状态，清空上次响应/错误/耗时；
   *   2. 非 GET 时解析 bodyText 为 JSON，解析失败则提示错误并返回；
   *   3. 构建请求对象（method/path/body/rawPayloadB64/contentType）；
   *   4. 调用 proxyRequest 发送到控制台后端代理；
   *   5. 成功时记录响应与耗时，失败时记录错误与耗时；
   *   6. 无论成败均记录历史并取消 loading。
   */
  const handleSend = async () => {
    setLoading(true);    // 开启加载态 / Enable loading state
    setError(null);      // 清空上次错误 / Clear previous error
    setResponse(null);   // 清空上次响应 / Clear previous response
    setDuration(null);   // 清空上次耗时 / Clear previous duration

    const start = performance.now(); // 记录开始时间戳 / Record start timestamp
    try {
      let body: Record<string, unknown> | undefined;
      // 非 GET 且请求体非空时解析 JSON / Parse JSON when not GET and body is non-empty
      if (method !== 'GET' && bodyText.trim()) {
        try {
          body = JSON.parse(bodyText); // 解析请求体 / Parse request body
        } catch (e) {
          // JSON 解析失败：提示用户并中止发送 / JSON parse failed: notify user and abort send
          setError(t('endpoint.json_parse_error', getErrorMessage(e)));
          setLoading(false);
          return;
        }
      }

      // 构建代理请求对象 / Build proxy request object
      const req = {
        method,                              // HTTP 方法 / HTTP method
        path,                                // 请求路径 / Request path
        body: body ?? null,                  // 解析后的请求体 / Parsed request body
        raw_payload_b64: sample.rawPayloadB64 ?? null, // 二进制载荷 Base64 / Binary payload Base64
        content_type: sample.contentType ?? null,      // 内容类型 / Content type
      };

      const res = await proxyRequest(req);   // 发送代理请求 / Send proxy request
      setResponse(res);                      // 设置响应数据 / Set response data
      setDuration(performance.now() - start); // 计算耗时 / Calculate duration
      recordHistory(res.status);             // 记录历史 / Record history
    } catch (e) {
      setError(getErrorMessage(e));        // 设置错误信息 / Set error message
      setDuration(performance.now() - start); // 记录失败耗时 / Record failure duration
      recordHistory(0);                      // 状态码 0 表示网络错误 / Status 0 indicates network error
    } finally {
      setLoading(false);                     // 取消加载态 / Disable loading state
    }
  };

  /**
   * 记录本次请求到历史 / Record current request to history
   *
   * GET 且空请求体时跳过，减少历史记录噪音。
   * Skips when GET with empty body to reduce history noise.
   *
   * @param status - HTTP 状态码（0 表示网络错误）/ HTTP status code (0 = network error)
   */
  const recordHistory = (status: number) => {
    if (method === 'GET' && !bodyText.trim()) return; // GET 无体时跳过 / Skip for GET without body
    setHistory(addHistory({ method, path, body: bodyText, status })); // 添加并更新状态 / Add and update state
  };

  /**
   * Cmd/Ctrl+Enter 快捷键发送 / Cmd/Ctrl+Enter Shortcut Send
   *
   * 使用 useRef 保存最新的 handleSend 引用，避免 useEffect 重复绑定。
   * Uses useRef to hold latest handleSend reference, avoiding useEffect re-binding.
   */
  const sendRef = useRef(handleSend); // 创建 ref 容器 / Create ref container
  sendRef.current = handleSend;       // 每次渲染更新为最新闭包 / Update to latest closure each render
  useEffect(() => {
    // 监听全局键盘事件 / Listen to global keyboard events
    const onKey = (e: KeyboardEvent) => {
      // 检测 Cmd(Mac)/Ctrl(Win) + Enter 组合键 / Detect Cmd(Mac)/Ctrl(Win) + Enter combo
      if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
        e.preventDefault();    // 阻止默认行为（如表单提交）/ Prevent default behavior (e.g. form submit)
        sendRef.current();     // 触发发送 / Trigger send
      }
    };
    window.addEventListener('keydown', onKey);       // 绑定事件 / Bind event
    return () => window.removeEventListener('keydown', onKey); // 清理事件 / Cleanup event
  }, []);

  /**
   * 重载示例请求 / Reload Sample Request
   *
   * 将 path/method/bodyText 恢复为当前端点的初始示例值，
   * 同时清空响应与错误，便于用户重新测试。
   * Restores path/method/bodyText to current endpoint's initial sample values,
   * clears response and error for fresh testing.
   */
  const handleLoadSample = () => {
    setPath(sample.path);              // 恢复路径 / Restore path
    setMethod(sample.method);          // 恢复方法 / Restore method
    setBodyText(formatJson(sample.body ?? {})); // 恢复示例请求体 / Restore sample body
    setResponse(null);                 // 清空响应 / Clear response
    setError(null);                    // 清空错误 / Clear error
  };


  /**
   * 一键格式化 / 校验请求体 JSON / One-click Format / Validate Request Body JSON
   *
   * 尝试解析并重新序列化（缩进 2 空格），解析失败则显示错误提示。
   * Attempts to parse and re-serialize (2-space indent), shows error if parse fails.
   */
  const handleFormat = () => {
    if (!bodyText.trim()) return; // 空内容无需格式化 / Empty content needs no formatting
    try {
      setBodyText(JSON.stringify(JSON.parse(bodyText), null, 2)); // 解析+重新格式化 / Parse + re-format
      setError(null); // 格式化成功时清除错误 / Clear error on successful format
    } catch (e) {
      setError(t('endpoint.json_format_error', getErrorMessage(e))); // 提示解析错误 / Show parse error
    }
  };

  /**
   * 生成 cURL 命令并复制到剪贴板 / Generate cURL Command and Copy to Clipboard
   *
   * 使用 buildCurl 拼装完整命令，通过 Clipboard API 写入剪贴板，
   * 复制成功后显示 1.5s 对勾反馈。
   * Uses buildCurl to assemble full command, writes to clipboard via Clipboard API,
   * shows 1.5s checkmark feedback on success.
   */
  const handleCopyCurl = async () => {
    // 构建 cURL：传入 method/path/body 及推导出的 agent 基址 / Build cURL: pass method/path/body and derived agent base URL
    const curl = buildCurl({ method, path, body: bodyText, baseUrl: deriveAgentBaseUrl(agentUrl) });
    try {
      await navigator.clipboard.writeText(curl); // 写入剪贴板 / Write to clipboard
      setCurlCopied(true);                       // 显示复制成功 / Show copy success
      setTimeout(() => setCurlCopied(false), 1500); // 1.5s 后恢复 / Restore after 1.5s
    } catch {
      /* 忽略剪贴板不可用（如非 HTTPS 环境）/ Ignore clipboard unavailable (e.g. non-HTTPS) */
    }
  };

  const meta = categoryMeta(sample.category); // 获取分类元数据（图标/配色）/ Get category metadata (icon/color)
  // 过滤出当前端点的历史记录（method+path 匹配）/ Filter history entries for current endpoint (method+path match)
  const endpointHistory = history.filter(
    (e) => e.method === sample.method && e.path === sample.path,
  );

  return (
    /* 根容器：占满全高，纵向弹性布局 / Root container: full height, vertical flex layout */
    <div className="flex h-full flex-col">
      {/* ====== 接口信息栏 / Endpoint Info Bar ====== */}
      {/* 不缩小、底部边框、白色背景 / Non-shrink, bottom border, white background */}
      <div className="shrink-0 border-b border-gray-200 bg-white px-5 py-3.5">
        <div className="flex items-center gap-3">
          {/* 返回按钮：点击回到总览页 / Back button: click returns to overview */}
          <button
            onClick={onBack}
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600"
            title={t('endpoint.back')}
            aria-label={t('endpoint.back')}
          >
            <Icon name="arrow-left" className="h-4 w-4" />
          </button>
          {/* HTTP 方法徽章：GET=绿色，POST=蓝色 / HTTP method badge: GET=green, POST=blue */}
          <span
            className={[
              'shrink-0 rounded-md px-2 py-1 text-xs font-bold',
              method === 'GET' ? 'bg-emerald-50 text-emerald-600' : 'bg-sky-50 text-sky-600',
            ].join(' ')}
          >
            {method}
          </span>
          {/* 可编辑路径输入框（等宽字体）/ Editable path input (monospace font) */}
          <input
            type="text"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            className="min-w-0 flex-1 rounded-md border border-gray-200 bg-gray-50 px-2.5 py-1.5 font-mono text-sm text-gray-800 transition-colors focus:border-indigo-400 focus:bg-white focus:outline-none focus:ring-2 focus:ring-indigo-100"
          />
          {/* 分类徽章（小屏隐藏）/ Category badge (hidden on small screens) */}
          <span
            className={`hidden shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium sm:inline-flex ${meta.chip}`}
          >
            <Icon name={meta.icon} className="h-3.5 w-3.5" />
            {sample.category}
          </span>
        </div>
        {/* 接口描述文字 / Endpoint description text */}
        <p className="mt-2 pl-10 text-sm text-gray-500">{sample.description}</p>
      </div>

      {/* ====== 左右分栏：请求编辑器 / 响应查看器 / Split Panes: Request Editor / Response Viewer ====== */}
      <div className="flex flex-1 overflow-hidden">
        {/* --- 左侧：请求编辑器（占 50% 宽度）/ Left: Request Editor (50% width) --- */}
        {/* relative 定位用于历史面板覆盖层 / Relative positioning for history panel overlay */}
        <div className="relative flex w-1/2 flex-col border-r border-gray-200 bg-white">
          {/* 工具栏：标题 + 操作按钮组 / Toolbar: title + action button group */}
          <div className="flex items-center justify-between border-b border-gray-100 px-4 py-2.5">
            {/* 标题："REQUEST BODY" / Title: "REQUEST BODY" */}
            <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500">{t('endpoint.request_body')}</h3>
            <div className="flex items-center gap-0.5">
              {/* 格式化按钮：GET 时禁用 / Format button: disabled for GET */}
              <button
                onClick={handleFormat}
                disabled={method === 'GET'}
                className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:cursor-not-allowed disabled:opacity-40"
                title={t('endpoint.format_title')}
              >
                <Icon name="code" className="h-3.5 w-3.5" />
                {t('endpoint.format')}
              </button>
              {/* cURL 复制按钮：复制成功后显示对勾 / cURL copy button: shows checkmark after copy */}
              <button
                onClick={handleCopyCurl}
                className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700"
                title={t('endpoint.curl_title')}
              >
                <Icon name={curlCopied ? 'check' : 'copy'} className="h-3.5 w-3.5" />
                {curlCopied ? t('endpoint.curl_copied') : 'cURL'}
              </button>
              {/* 历史按钮：切换历史面板显示/隐藏 / History button: toggle history panel show/hide */}
              <button
                onClick={() => setShowHistory((v) => !v)}
                className={[
                  'inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs transition-colors',
                  /* 展开时靛蓝高亮 / Indigo highlight when expanded */
                  showHistory
                    ? 'bg-indigo-50 text-indigo-600'
                    : 'text-gray-500 hover:bg-gray-100 hover:text-gray-700',
                ].join(' ')}
                title={t('endpoint.history_title')}
              >
                <Icon name="clock" className="h-3.5 w-3.5" />
                {t('endpoint.history')}
              </button>
              {/* 重载示例按钮：恢复初始请求体 / Reload sample button: restore initial request body */}
              <button
                onClick={handleLoadSample}
                className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700"
                title={t('endpoint.reload_title')}
              >
                <Icon name="refresh" className="h-3.5 w-3.5" />
                {t('endpoint.reload_sample')}
              </button>
            </div>
          </div>

          {/* 请求体编辑区域 / Request Body Editing Area */}
          <div className="flex flex-1 flex-col overflow-hidden p-4">
            {/* JSON 编辑文本域：GET 时禁用、等宽字体、关闭拼写检查 / JSON editor textarea: disabled for GET, monospace, spellcheck off */}
            <textarea
              value={bodyText}
              onChange={(e) => setBodyText(e.target.value)}
              disabled={method === 'GET'}
              spellCheck={false}
              className={[
                'flex-1 resize-none rounded-lg border border-gray-200 p-3 font-mono text-xs leading-relaxed transition-colors focus:outline-none focus:ring-2 focus:ring-indigo-100',
                /* GET 时灰色置灰样式 / Gray disabled style for GET */
                method === 'GET'
                  ? 'bg-gray-50 text-gray-400'
                  : 'bg-gray-50/50 text-gray-800 focus:border-indigo-400 focus:bg-white',
              ].join(' ')}
              placeholder={method === 'GET' ? t('endpoint.get_no_body') : '{ }'}
            />
            {/* 二进制载荷提示：显示 Content-Type / Binary payload hint: shows Content-Type */}
            {sample.contentType && (
              <p className="mt-2 text-[11px] text-gray-400">
                {t('endpoint.content_type_hint', sample.contentType)}
              </p>
            )}

            {/* 发送按钮：加载中禁用并显示“发送中” / Send button: disabled during loading, shows "Sending" */}
            <button
              onClick={handleSend}
              disabled={loading}
              className={[
                'mt-3 inline-flex h-9 items-center justify-center gap-2 rounded-lg text-sm font-medium text-white shadow-sm transition-colors',
                /* 加载中降低不透明度+禁止光标 / Loading: reduced opacity + not-allowed cursor */
                loading ? 'cursor-not-allowed bg-indigo-400' : 'bg-indigo-600 hover:bg-indigo-700',
              ].join(' ')}
              title={t('endpoint.send_shortcut')}
            >
              <Icon name="send" className="h-3.5 w-3.5" />
              {loading ? t('endpoint.sending') : t('endpoint.send')}
              {/* 快捷键提示徽章（小屏隐藏）/ Shortcut hint badge (hidden on small screens) */}
              <kbd className="ml-1 hidden rounded bg-indigo-500/40 px-1.5 py-0.5 text-[10px] font-normal text-indigo-100 sm:inline">
                ⌘↵
              </kbd>
            </button>
          </div>

          {/* 历史面板（绝对定位覆盖请求编辑区）/ History Panel (absolutely positioned overlay on request editor) */}
          {showHistory && (
            <HistoryPanel
              entries={endpointHistory}
              onRestore={(body) => {
                setBodyText(body);       // 回填历史请求体 / Restore history request body
                setShowHistory(false);   // 关闭历史面板 / Close history panel
              }}
              onDelete={(id) => setHistory(removeHistory(id))}  // 删除单条 / Delete single entry
              onClear={() => setHistory(clearHistory())}        // 清空全部 / Clear all
              onClose={() => setShowHistory(false)}             // 关闭面板 / Close panel
            />
          )}
        </div>

        {/* --- 右侧：响应查看器（占 50% 宽度）/ Right: Response Viewer (50% width) --- */}
        <div className="flex w-1/2 flex-col bg-white">
          <ResponsePanel response={response} error={error} duration={duration} path={path} />
        </div>
      </div>
    </div>
  );
}
