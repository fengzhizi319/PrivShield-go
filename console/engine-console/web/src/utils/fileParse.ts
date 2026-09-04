/**
 * 前端数据文件解析器（用于“原始文件预览”）/ Frontend Data File Parser (for "Original File Preview")
 *
 * 与后端 ``fileparse``（Go）/ agent 的 records 接口语义保持一致：
 * Semantically consistent with backend ``fileparse`` (Go) / agent records interface:
 *   - CSV：首行视为表头（schema），其余行按表头列名映射为记录，
 *     某行字段数不足时以空字符串补齐，允许各行字段数不一致；
 *     支持引号字段（含逗号 / 换行 / 转义引号 ``""``），忽略空行；
 *   - CSV: first row as header (schema), remaining rows mapped by header column names,
 *     missing fields padded with empty string, variable field count per row allowed;
 *     supports quoted fields (with commas / newlines / escaped quotes ``""``), ignores empty lines;
 *   - JSON：需为“记录对象数组”，schema 取所有记录出现过的键并按字母序排序；
 *     每个值统一转换为字符串（数字 / 布尔 / null / 嵌套对象均有对应处理）。
 *   - JSON: must be "array of record objects", schema collects all keys across records sorted alphabetically;
 *     each value uniformly converted to string (number / boolean / null / nested object all handled).
 *
 * 值统一转字符串是为了与后端 records（map[string]string）的语义对齐，
 * 从而保证“原始数据”与“处理结果”两表可以做逐行逐列的对比。
 * Uniform string conversion aligns with backend records (map[string]string) semantics,
 * ensuring "original data" and "processed result" tables can be compared row by row and column by column.
 */

/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * 前端文件解析大小上限（50 MB）。
 * Maximum file size for frontend parsing (50 MB).
 *
 * 超过此限制的文件可能导致浏览器内存溢出或长时间卡顿，
 * 生产环境应通过后端批量处理大文件。
 * Files exceeding this limit may cause browser OOM or long stalls;
 * production environments should process large files via backend batch processing.
 */
const MAX_PARSE_FILE_SIZE = 50 * 1024 * 1024; // 50 MB

/** 解析后的统一结构：记录数组 + 列名顺序 / Parsed unified structure: records array + column order */
export interface ParsedRecords {
  /** 每条记录：列名 → 字符串值 / Each record: column name → string value */
  records: Record<string, string>[];
  /** 列名顺序（CSV 为表头顺序，JSON 为字母序）/ Column order (CSV = header order, JSON = alphabetical) */
  schema: string[];
}

/**
 * 把 CSV 文本解析为二维字符串数组（含表头行）/ Parse CSV text into 2D string array (including header row)
 *
 * 采用状态机实现，支持 / Implemented with state machine, supports：
 *   - 引号字段内的逗号、换行与转义引号（``""`` → ``"``）；
 *     Commas, newlines and escaped quotes within quoted fields (``""`` → ``"``);
 *   - ``\r\n`` / ``\n`` / ``\r`` 三种换行 / Three newline variants;
 *   - 忽略空行（与 Go ``encoding/csv`` 行为一致）/ Ignore empty lines (consistent with Go ``encoding/csv``);
 *   - 去除 UTF-8 BOM / Strip UTF-8 BOM.
 */
function parseCsvRows(text: string): string[][] {
  // 去除可能存在的 UTF-8 BOM，避免首列列名被污染。
  if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);

  const rows: string[][] = [];
  let row: string[] = [];
  let field = '';
  let inQuotes = false;
  let i = 0;
  const n = text.length;

  /** 结束当前行：把累积的字段压入行，再把行压入结果。 */
  const endRow = () => {
    row.push(field);
    field = '';
    rows.push(row);
    row = [];
  };

  while (i < n) {
    const ch = text[i];
    if (inQuotes) {
      if (ch === '"') {
        // 连续两个引号表示转义的字面引号。
        if (i + 1 < n && text[i + 1] === '"') {
          field += '"';
          i += 2;
        } else {
          inQuotes = false;
          i++;
        }
      } else {
        field += ch;
        i++;
      }
      continue;
    }
    if (ch === '"') {
      inQuotes = true;
      i++;
    } else if (ch === ',') {
      row.push(field);
      field = '';
      i++;
    } else if (ch === '\n' || ch === '\r') {
      // 空行（行内无任何内容）直接跳过，与 Go encoding/csv 保持一致。
      if (row.length === 0 && field === '') {
        i++;
        continue;
      }
      endRow();
      // \r\n 视为一个换行。
      if (ch === '\r' && i + 1 < n && text[i + 1] === '\n') i += 2;
      else i++;
    } else {
      field += ch;
      i++;
    }
  }
  // 文件末尾若无换行收尾，需把最后一行刷出。
  if (field !== '' || row.length > 0) {
    endRow();
  }
  return rows;
}

/** 把 CSV 文本解析为 records + schema（首行为表头）/ Parse CSV text into records + schema (first row as header) */
function parseCsvRecords(text: string): ParsedRecords {
  const rows = parseCsvRows(text);
  if (rows.length === 0) {
    throw new Error('CSV 文件为空');
  }
  const schema = rows[0];
  const records = rows.slice(1).map((row) => {
    const rec: Record<string, string> = {};
    // 按表头列名映射；字段数不足时以空串补齐（与后端一致）。
    schema.forEach((col, idx) => {
      rec[col] = idx < row.length ? row[idx] : '';
    });
    return rec;
  });
  return { records, schema };
}

/** 把任意 JSON 值统一转换为字符串表示（与后端 toString 语义对齐）/ Convert any JSON value to string representation (aligned with backend toString semantics) */
function toJsonString(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'string') return v;
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  // 嵌套对象 / 数组：序列化为紧凑 JSON 字符串。
  return JSON.stringify(v);
}

/** 把 JSON 记录数组解析为 records + schema（schema 按字母序排序）/ Parse JSON record array into records + schema (schema sorted alphabetically) */
function parseJsonRecords(text: string): ParsedRecords {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (e) {
    throw new Error(`JSON 解析失败（需为记录数组）: ${getErrorMessage(e)}`);
  }
  if (!Array.isArray(raw)) {
    throw new Error('JSON 需为记录对象数组');
  }

  // 收集所有记录出现过的键作为 schema，并按字母序排序保证结果确定。
  const keySet = new Set<string>();
  for (const obj of raw) {
    if (obj && typeof obj === 'object' && !Array.isArray(obj)) {
      Object.keys(obj as Record<string, unknown>).forEach((k) => keySet.add(k));
    }
  }
  const schema = Array.from(keySet).sort();

  const records = raw.map((obj) => {
    const rec: Record<string, string> = {};
    if (obj && typeof obj === 'object' && !Array.isArray(obj)) {
      for (const [k, v] of Object.entries(obj as Record<string, unknown>)) {
        rec[k] = toJsonString(v);
      }
    }
    return rec;
  });
  return { records, schema };
}

/**
 * 解析上传的 CSV/JSON 文件为 records + schema / Parse uploaded CSV/JSON file into records + schema
 *
 * 仅依据文件扩展名选择解析方式，与后端按扩展名路由的逻辑一致；
 * Selects parsing method solely by file extension, consistent with backend extension-based routing;
 * 解析失败时抛出带中文说明的 Error，由调用方展示。
 * Throws Error with Chinese description on parse failure, displayed by caller.
 *
 * @param file - 待解析的文件 / File to parse
 * @returns 解析结果 / Parsed result
 */
export async function parseDataFile(file: File): Promise<ParsedRecords> {
  // P47 fix: reject oversized files before reading to prevent browser OOM / freeze.
  // 在读取前拒绝超大文件，防止浏览器内存溢出或卡顿。
  if (file.size > MAX_PARSE_FILE_SIZE) {
    const maxMB = Math.round(MAX_PARSE_FILE_SIZE / (1024 * 1024));
    throw new Error(`文件过大（${(file.size / (1024 * 1024)).toFixed(1)} MB），超过前端解析上限 ${maxMB} MB，请使用后端批量处理接口`);
  }
  const text = await file.text();
  const name = file.name.toLowerCase();
  if (name.endsWith('.csv')) return parseCsvRecords(text);
  if (name.endsWith('.json')) return parseJsonRecords(text);
  throw new Error('仅支持 .csv 与 .json 文件');
}
