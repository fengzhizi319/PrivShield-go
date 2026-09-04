/**
 * 数据文件隐私处理视图 / Data File Privacy Processing View
 *
 * 用户上传 CSV/JSON 文件，选择操作类型（脱敏 / K-匿名），
 * 按操作动态填写参数，提交后经后端转发到 agent 处理。
 * 右侧上方展示原始响应（复用 ResponsePanel），下方以“原始数据 / 处理结果”
 * 双表并排呈现，并对发生变更的单元格高亮，便于直观对比处理前后的差异。
 * User uploads CSV/JSON file, selects operation type (masking / K-anonymity),
 * fills parameters dynamically per operation, submits via backend proxy to agent.
 * Right top shows raw response (reuses ResponsePanel), bottom shows "original / result"
 * side-by-side tables with changed cells highlighted for intuitive before/after comparison.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 左侧配置表单：文件选择 + 示例文件快捷填充 + 操作类型 + 动态参数；
 *   2. 文件变化时客户端解析为 records+schema（仅用于预览，不影响上传）；
 *   3. 提交时调用 uploadFile API（multipart/form-data）；
 *   4. 右侧上方 ResponsePanel 展示原始 JSON，下方双表对比（变更单元格琥珀色高亮）。
 */

/** 引入 React Hooks / Import React Hooks */
import { useEffect, useMemo, useRef, useState } from 'react';
/** 引入类型定义 / Import type definitions */
import type { FileOperation, UploadResponse } from '@/types/api';
/** 引入文件上传 API / Import file upload API */
import { uploadFile } from '@/api/client';
/** 引入响应面板组件 / Import response panel component */
import ResponsePanel from '@/components/ResponsePanel';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入通用动作按钮 / Import generic action button */
import ActionButton from '@/components/ActionButton';
/** 引入示例文件工具 / Import sample file utilities */
import { createSampleFile, downloadSampleFile, type SampleFormat } from '@/utils/sampleFile';
/** 引入文件解析工具 / Import file parsing utility */
import { parseDataFile, type ParsedRecords } from '@/utils/fileParse';
/** 引入 i18n Hook / Import i18n Hook */
import { useI18n } from '@/i18n';
/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * 操作选项元数据 / Operation Option Metadata
 *
 * 定义支持的文件处理操作；label/hint 通过 i18n key 在渲染时翻译，
 * 避免在模块顶层固化某种自然语言。
 * Defines supported file processing operations; label/hint are translated
 * at render time via i18n keys, avoiding hard-coding any natural language at module top level.
 */
const OPERATIONS: { value: FileOperation; labelKey: string; hintKey: string }[] = [
  { value: 'mask_dataframe', labelKey: 'file.op_mask', hintKey: 'file.op_mask_hint' }, // 脱敏 / Masking
  { value: 'k_anonymize', labelKey: 'file.op_kano', hintKey: 'file.op_kano_hint' },    // K-匿名 / K-anonymity
];

/**
 * 拆分逗号分隔的列名输入 / Split comma-separated column name input
 *
 * 支持中文逗号、英文逗号、空白分隔，去除空项。
 * Supports Chinese comma, English comma, whitespace separators, removes empty items.
 *
 * @param text - 用户输入的列名字符串 / User input column names string
 * @returns 列名数组 / Column names array
 */
function splitCols(text: string): string[] {
  return text
    .split(/[,，\s]+/)    // 按逗号/空白拆分 / Split by comma/whitespace
    .map((s) => s.trim()) // 去除首尾空白 / Trim each item
    .filter(Boolean);     // 移除空项 / Remove empty items
}

/** 表格预览的最大行数，避免大文件渲染过多 DOM / Max preview rows to avoid excessive DOM for large files */
const MAX_PREVIEW_ROWS = 50;

/** 客户端上传大小上限（10MB，与后端 CONSOLE_MAX_UPLOAD_BYTES 默认值一致）/ Client upload size limit (10MB, matches backend CONSOLE_MAX_UPLOAD_BYTES default) */
export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024;

/** 支持的文件扩展名 / Supported file extensions */
export const ACCEPTED_EXTS = ['.csv', '.json'];

/**
 * 文件校验错误码 / File Validation Error Code
 *
 * 与展示解耦：validateFile 仅返回结构化错误码与上下文参数，
 * 具体文案由调用方通过 i18n 翻译，保证校验逻辑可独立测试。
 * Decoupled from presentation: validateFile returns only a structured error code
 * with contextual params; the actual message is translated by the caller via i18n,
 * keeping validation logic independently testable.
 */
export type FileValidationErrorCode = 'unsupported_ext' | 'too_large';

/** 文件校验错误结构 / File validation error structure */
export interface FileValidationError {
  /** 错误码 / Error code */
  code: FileValidationErrorCode;
  /** too_large 场景：实际大小（MB，保留 1 位小数）/ Actual size (MB, 1 decimal) for too_large */
  sizeMb?: string;
  /** too_large 场景：大小上限（MB）/ Size limit (MB) for too_large */
  limitMb?: number;
}

/**
 * 客户端预校验文件类型与大小 / Client-side pre-validation of file type and size
 *
 * 在上传前提前拦截不合规文件，避免无效的大文件/错误格式
 * 消耗网络与后端资源（与后端 413/400 校验互为双保险）。
 * Pre-intercepts non-compliant files before upload, avoiding invalid large files/wrong formats
 * consuming network and backend resources (dual insurance with backend 413/400 validation).
 *
 * @param f - 待校验的文件 / File to validate
 * @returns 结构化错误；合法时返回 null / Structured error; null when valid
 */
export function validateFile(f: File): FileValidationError | null {
  const lower = f.name.toLowerCase(); // 文件名转小写 / Lowercase filename
  // 检查扩展名是否支持 / Check if extension is supported
  if (!ACCEPTED_EXTS.some((ext) => lower.endsWith(ext))) {
    return { code: 'unsupported_ext' };
  }
  // 检查文件大小是否超限 / Check if file size exceeds limit
  if (f.size > MAX_UPLOAD_BYTES) {
    return {
      code: 'too_large',
      sizeMb: (f.size / 1024 / 1024).toFixed(1),
      limitMb: MAX_UPLOAD_BYTES / 1024 / 1024,
    };
  }
  return null; // 校验通过 / Validation passed
}

/**
 * 通用记录表格组件 / Generic Record Table Component
 *
 * 按 schema 列序渲染记录数组。
 * Renders record array by schema column order.
 *
 * 传入 ``baseline``（原始记录）时，会逐行逐列对比，
 * 将“处理后与原始值不同”的单元格高亮为琥珀色，
 * 从而直观呈现脱敏 / K-匿名等操作带来的变化。
 * When ``baseline`` (original records) is provided, compares row by row and column by column,
 * highlighting cells that differ from original in amber color,
 * intuitively showing changes from masking / K-anonymity operations.
 */
function DataTable({
  records,
  schema,
  baseline = null,
}: {
  records: Record<string, unknown>[];
  /** 列名顺序；省略时从记录中推导。 */
  schema?: string[];
  /** 对比基准（原始记录），用于高亮变更单元格。 */
  baseline?: Record<string, unknown>[] | null;
}) {
  const cols = useMemo(() => {
    if (schema && schema.length > 0) return schema;
    const set = new Set<string>();
    records.forEach((r) => {
      if (r && typeof r === 'object') Object.keys(r).forEach((k) => set.add(k));
    });
    return Array.from(set);
  }, [records, schema]);

  if (records.length === 0 || cols.length === 0) return null;
  const preview = records.slice(0, MAX_PREVIEW_ROWS);

  return (
    <table className="w-full border-collapse text-xs">
      <thead>
        <tr>
          {cols.map((c) => (
            <th
              key={c}
              className="sticky top-0 z-10 border border-gray-200 bg-gray-50 px-2 py-1 text-left font-semibold text-gray-600"
            >
              {c}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {preview.map((row, i) => (
          <tr key={i} className="hover:bg-indigo-50/40">
            {cols.map((c) => {
              const val = row?.[c] ?? '';
              // 与原始记录同一行同一列对比，值不同则高亮。
              const baseRow = baseline?.[i];
              const changed =
                !!baseline && baseRow !== undefined && String(baseRow[c] ?? '') !== String(val);
              return (
                <td
                  key={c}
                  className={
                    changed
                      ? 'border border-amber-200 bg-amber-100 px-2 py-1 font-medium text-amber-900'
                      : 'border border-gray-100 px-2 py-1 text-gray-700'
                  }
                >
                  {String(val)}
                </td>
              );
            })}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/**
 * 数据文件隐私处理主组件 / Data File Privacy Processing Main Component
 *
 * 左侧配置表单（文件/操作/参数），右侧结果展示（响应 + 双表对比）。
 * Left config form (file/operation/params), right result display (response + dual table comparison).
 */
export default function FileTest() {
  /** i18n 翻译函数 / i18n translation function */
  const { t } = useI18n();
  const [file, setFile] = useState<File | null>(null);
  const [operation, setOperation] = useState<FileOperation>('mask_dataframe');
  // 各操作的参数输入
  const [columns, setColumns] = useState('email, phone');
  const [context, setContext] = useState('');
  const [qiCols, setQiCols] = useState('age, zip, gender');
  const [k, setK] = useState(2);

  const [loading, setLoading] = useState(false);
  const [response, setResponse] = useState<UploadResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  /** 原始文件解析结果（用于“原始数据”预览与差异对比）。 */
  const [original, setOriginal] = useState<ParsedRecords | null>(null);
  /** 原始文件解析失败的提示（不影响上传，仅预览不可用）。 */
  const [parseError, setParseError] = useState<string | null>(null);

  const opMeta = useMemo(() => OPERATIONS.find((o) => o.value === operation)!, [operation]);

  /**
   * 将结构化校验错误翻译为当前语言的提示文案。
   * Translate a structured validation error into a message in the current language.
   */
  const validationMessage = (err: FileValidationError): string =>
    err.code === 'unsupported_ext'
      ? t('file.err_ext')
      : t('file.err_size', err.sizeMb ?? '', err.limitMb ?? 0);

  /**
   * 文件变化时在浏览器端解析为 records + schema，供“原始数据”预览。
   *
   * 该解析仅用于界面展示，与上传后后端的解析相互独立；
   * 解析失败只提示预览不可用，不阻止上传。
   */
  useEffect(() => {
    let cancelled = false;
    if (!file) {
      setOriginal(null);
      setParseError(null);
      return;
    }
    parseDataFile(file)
      .then((parsed) => {
        if (!cancelled) {
          setOriginal(parsed);
          setParseError(null);
        }
      })
      .catch((e) => {
        if (!cancelled) {
          setOriginal(null);
          setParseError(getErrorMessage(e));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [file]);

  /** 处理结果中的记录数组（仅当 result 为非空数组时，脱敏 / K-匿名场景）。 */
  const resultRecords = useMemo(() => {
    const r = response?.data?.result;
    return Array.isArray(r) && r.length > 0 ? (r as Record<string, unknown>[]) : null;
  }, [response]);

  /**
   * 结果表的列序：以原始 schema 为准（保证两表列序一致、可并排对比），
   * 再追加结果中多出的列；原始解析不可用时退化为从结果推导。
   */
  const resultCols = useMemo(() => {
    if (!resultRecords) return [];
    const base = original?.schema ?? [];
    const extra = new Set<string>();
    resultRecords.forEach((r) => {
      if (r && typeof r === 'object') {
        Object.keys(r).forEach((k) => {
          if (!base.includes(k)) extra.add(k);
        });
      }
    });
    return [...base, ...Array.from(extra)];
  }, [resultRecords, original]);

  /** 根据当前操作组装 params 对象。 */
  function buildParams(): Record<string, unknown> {
    switch (operation) {
      case 'mask_dataframe':
        return { columns: splitCols(columns), context };
      case 'k_anonymize':
        return { qi_cols: splitCols(qiCols), k };
      default:
        return {};
    }
  }

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0] ?? null;
    setResponse(null);
    setError(null);
    // 客户端预校验类型 / 大小；不合规时拒绝并清空选择。
    if (f) {
      const problem = validateFile(f);
      if (problem) {
        setError(validationMessage(problem));
        setFile(null);
        e.target.value = '';
        return;
      }
    }
    setFile(f);
  };

  /**
   * 一键填充预生成的示例文件。
   *
   * 在内存中构造与磁盘文件等价的 ``File`` 对象并填入上传控件，
   * 用户无需手工准备测试数据即可直接点击“上传并处理”。
   */
  const handleUseSample = (format: SampleFormat) => {
    setFile(createSampleFile(format));
    setResponse(null);
    setError(null);
  };

  const handleSubmit = async () => {
    if (!file) {
      setError(t('file.no_file'));
      return;
    }
    setLoading(true);
    setError(null);
    setResponse(null);
    try {
      const resp = await uploadFile(file, operation, buildParams());
      setResponse(resp);
    } catch (e) {
      setError(getErrorMessage(e));
    } finally {
      setLoading(false);
    }
  };

  const inputCls =
    'w-full rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-sm text-gray-700 placeholder-gray-400 transition-colors focus:border-indigo-400 focus:bg-white focus:outline-none focus:ring-2 focus:ring-indigo-100';

  return (
    <div className="flex h-full">
      {/* 左侧：配置表单 */}
      <div className="flex w-[380px] shrink-0 flex-col gap-4 overflow-y-auto border-r border-gray-200 bg-white p-5">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold text-gray-800">
            <span className="flex h-6 w-6 items-center justify-center rounded bg-indigo-50 text-indigo-600">
              <Icon name="upload" className="h-3.5 w-3.5" />
            </span>
            {t('file.title')}
          </h2>
          <p className="mt-1 text-xs text-gray-500">{t('file.subtitle')}</p>
        </div>

        {/* 文件选择 */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">{t('file.file_label')}</label>
          <input
            ref={fileInputRef}
            type="file"
            accept=".csv,.json"
            onChange={handleFileChange}
            className="block w-full text-sm text-gray-600 file:mr-3 file:rounded-lg file:border-0 file:bg-indigo-50 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-indigo-600 hover:file:bg-indigo-100"
          />
          {file && (
            <p className="mt-1 text-xs text-gray-400">
              {t('file.selected', file.name, (file.size / 1024).toFixed(1))}
            </p>
          )}
          {/* 示例文件：免手工准备数据，一键填充或下载 */}
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            <span className="text-xs text-gray-400">{t('file.sample_prefix')}</span>
            {(['csv', 'json'] as SampleFormat[]).map((fmt) => (
              <span key={fmt} className="inline-flex items-center overflow-hidden rounded-md border border-indigo-200">
                <button
                  onClick={() => handleUseSample(fmt)}
                  className="px-2 py-0.5 text-xs font-medium text-indigo-600 transition-colors hover:bg-indigo-50"
                  title={t('file.sample_fill_title', fmt.toUpperCase())}
                >
                  {fmt.toUpperCase()}
                </button>
                <button
                  onClick={() => downloadSampleFile(fmt)}
                  className="border-l border-indigo-200 px-1.5 py-0.5 text-indigo-400 transition-colors hover:bg-indigo-50 hover:text-indigo-600"
                  title={t('file.sample_download_title', fmt.toUpperCase())}
                >
                  <Icon name="download" className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>
        </div>

        {/* 操作选择 */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">{t('file.op_label')}</label>
          <select
            value={operation}
            onChange={(e) => setOperation(e.target.value as FileOperation)}
            className={inputCls}
          >
            {OPERATIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {t(o.labelKey)}
              </option>
            ))}
          </select>
          <p className="mt-1 text-xs text-gray-400">{t(opMeta.hintKey)}</p>
        </div>

        {/* 动态参数 */}
        {operation === 'mask_dataframe' && (
          <>
            <div>
              <label className="mb-1 block text-xs font-medium text-gray-600">{t('file.mask_cols')}</label>
              <input value={columns} onChange={(e) => setColumns(e.target.value)} className={inputCls} placeholder="email, phone" />
            </div>
            <div>
              <label className="mb-1 block text-xs font-medium text-gray-600">{t('file.mask_context')}</label>
              <input value={context} onChange={(e) => setContext(e.target.value)} className={inputCls} placeholder={t('file.mask_context_ph')} />
            </div>
          </>
        )}

        {operation === 'k_anonymize' && (
          <>
            <div>
              <label className="mb-1 block text-xs font-medium text-gray-600">{t('file.qi_cols')}</label>
              <input value={qiCols} onChange={(e) => setQiCols(e.target.value)} className={inputCls} placeholder="age, zip, gender" />
            </div>
            <div>
              <label className="mb-1 block text-xs font-medium text-gray-600">{t('file.k_value')}</label>
              <input
                type="number"
                min={2}
                value={k}
                onChange={(e) => setK(Math.max(2, Number(e.target.value) || 2))}
                className={inputCls}
              />
            </div>
          </>
        )}

        <ActionButton
          onClick={handleSubmit}
          loading={loading}
          icon="send"
          loadingText={t('file.submitting')}
        >
          {t('file.submit')}
        </ActionButton>
      </div>

      {/* 右侧：上方原始响应，下方“原始数据 / 处理结果”并排对比 */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* 上方：原始响应 JSON（含后端 / 协议徽章与错误展示） */}
        <div className="h-[34%] min-h-[150px] shrink-0 overflow-hidden border-b border-gray-200">
          <ResponsePanel response={response} error={error} duration={null} path="upload" />
        </div>

        {/* 下方：处理前后对比 */}
        <div className="flex flex-1 overflow-hidden">
          {/* 原始数据 */}
          <section className="flex w-1/2 flex-col overflow-hidden border-r border-gray-200">
            <header className="flex shrink-0 items-center justify-between border-b border-gray-100 bg-gray-50/70 px-4 py-2">
              <span className="text-xs font-semibold text-gray-600">{t('file.original')}</span>
              {original && (
                <span className="text-[11px] text-gray-400">
                  {t('file.rows_preview', Math.min(original.records.length, MAX_PREVIEW_ROWS), original.records.length)}
                </span>
              )}
            </header>
            <div className="flex-1 overflow-auto p-2">
              {original ? (
                <DataTable records={original.records} schema={original.schema} />
              ) : (
                <div className="flex h-full items-center justify-center px-6 text-center text-xs text-gray-400">
                  {parseError ? t('file.preview_unavailable', parseError) : t('file.original_empty')}
                </div>
              )}
            </div>
          </section>

          {/* 处理结果 */}
          <section className="flex w-1/2 flex-col overflow-hidden">
            <header className="flex shrink-0 items-center justify-between gap-2 border-b border-gray-100 bg-gray-50/70 px-4 py-2">
              <span className="text-xs font-semibold text-gray-600">{t('file.result')}</span>
              <span className="flex items-center gap-2 text-[11px] text-gray-400">
                {resultRecords && (
                  <span>
                    {t('file.rows_preview', Math.min(resultRecords.length, MAX_PREVIEW_ROWS), resultRecords.length)}
                  </span>
                )}
                {/* 差异图例：琥珀色 = 相比原始数据发生变更 */}
                <span className="inline-flex items-center gap-1">
                  <span className="inline-block h-2.5 w-2.5 rounded-sm border border-amber-300 bg-amber-100" />
                  {t('file.changed')}
                </span>
              </span>
            </header>
            <div className="flex-1 overflow-auto p-2">
              {resultRecords ? (
                <DataTable records={resultRecords} schema={resultCols} baseline={original?.records ?? null} />
              ) : (
                <div className="flex h-full items-center justify-center px-6 text-center text-xs text-gray-400">
                  {response
                    ? t('file.result_no_records')
                    : t('file.result_empty')}
                </div>
              )}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
