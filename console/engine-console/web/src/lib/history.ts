/** 导入历史记录类型定义（包含 id/method/path/body/status/timestamp 字段） */
/** Import the HistoryEntry type definition (contains id/method/path/body/status/timestamp fields) */
import type { HistoryEntry } from '@/types/api';

/**
 * 请求历史记录模块 / Request History Module
 *
 * 功能：持久化到 localStorage，供 EndpointView 的“历史”面板使用。
 * 仅保存请求体文本与状态码，不保存响应（避免存储过大）。
 *
 * Function: Persist to localStorage for the EndpointView "History" panel.
 * Only saves request body text and status code, not the response (to avoid excessive storage).
 *
 * 设计约束 / Design Constraints：
 *   - 最多保存 MAX_ENTRIES 条记录，超出时截断最早的记录；
 *   - 存储不可用（如浏览器隐私模式）时静默降级，不影响主流程。
 */

/** localStorage 存储键名 / localStorage storage key name */
const STORAGE_KEY = 'privacy-console.history';
/** 历史记录最大条数上限 / Maximum number of history entries allowed */
const MAX_ENTRIES = 50;

/**
 * 从 localStorage 加载全部历史记录 / Load all history entries from localStorage
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 尝试读取 localStorage 中的 JSON 字符串；
 *   2. 解析并校验是否为数组，是则返回，否则返回空数组；
 *   3. 任何异常（存储不可用 / JSON 损坏）均静默返回空数组。
 *
 * @returns 历史记录数组（可能为空）/ History entries array (may be empty)
 */
export function loadHistory(): HistoryEntry[] {
  try {
    // 从 localStorage 读取原始字符串 / Read raw string from localStorage
    const raw = localStorage.getItem(STORAGE_KEY);
    // 键不存在时返回空数组 / Return empty array if key doesn't exist
    if (!raw) return [];
    // 解析 JSON 字符串为 JS 对象 / Parse JSON string into JS object
    const parsed = JSON.parse(raw);
    // 类型守卫：确保解析结果是数组，否则视为损坏数据返回空 / Type guard: ensure parsed result is array, otherwise treat as corrupted
    return Array.isArray(parsed) ? (parsed as HistoryEntry[]) : [];
  } catch {
    // 存储不可用或 JSON 解析失败，静默降级 / Storage unavailable or JSON parse failed, silently degrade
    return [];
  }
}

/**
 * 将历史记录数组序列化并写入 localStorage / Serialize history entries and write to localStorage
 *
 * @param entries - 要保存的完整历史记录数组 / The complete history entries array to save
 */
function saveHistory(entries: HistoryEntry[]): void {
  try {
    // 序列化为 JSON 字符串并写入 / Serialize to JSON string and write
    localStorage.setItem(STORAGE_KEY, JSON.stringify(entries));
  } catch {
    /* 存储不可用（如隐私模式）时静默降级，不抛出异常 */
    /* Silently degrade when storage is unavailable (e.g. privacy mode), don't throw */
  }
}

/**
 * 新增一条历史记录 / Add a new history entry
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 自动生成唯一 id（时间戳 + 随机字符串）和 timestamp；
 *   2. 新记录置顶（unshift 语义），与已有历史合并；
 *   3. 截断到 MAX_ENTRIES 条，避免无限增长；
 *   4. 持久化并返回更新后的完整列表。
 *
 * @param entry - 不含 id/timestamp 的历史记录字段 / History entry fields without id/timestamp
 * @returns 更新后的完整历史记录列表 / Updated complete history list
 */
export function addHistory(entry: Omit<HistoryEntry, 'id' | 'timestamp'>): HistoryEntry[] {
  // 构造完整的历史记录对象，自动生成唯一 ID 和时间戳
  // Construct the full history entry object with auto-generated unique ID and timestamp
  const full: HistoryEntry = {
    ...entry, // 展开 method/path/body/status 等字段 / Spread method/path/body/status fields
    // 唯一 ID：毫秒时间戳 + 6位随机 base36 字符串，保证碰撞率极低
    // Unique ID: millisecond timestamp + 6-char random base36 string, extremely low collision rate
    id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    // 当前时间的毫秒时间戳 / Current time in milliseconds timestamp
    timestamp: Date.now(),
  };
  // 新记录置顶 + 合并已有历史 + 截断到上限 / Prepend new entry + merge existing + truncate to limit
  const next = [full, ...loadHistory()].slice(0, MAX_ENTRIES);
  // 持久化到 localStorage / Persist to localStorage
  saveHistory(next);
  // 返回更新后的列表供组件直接 setState / Return updated list for component to setState directly
  return next;
}

/**
 * 按 id 删除一条历史记录 / Remove a history entry by id
 *
 * @param id - 要删除的历史记录唯一标识 / The unique identifier of the entry to remove
 * @returns 删除后的历史记录列表 / History list after removal
 */
export function removeHistory(id: string): HistoryEntry[] {
  // 过滤掉目标 id 的记录 / Filter out the entry with target id
  const next = loadHistory().filter((e) => e.id !== id);
  // 持久化更新后的列表 / Persist the updated list
  saveHistory(next);
  return next;
}

/**
 * 清空全部历史记录 / Clear all history entries
 *
 * @returns 空数组 / Empty array
 */
export function clearHistory(): HistoryEntry[] {
  // 写入空数组即清空 / Writing empty array clears all
  saveHistory([]);
  return [];
}

/**
 * 相对时间展示 / Relative time display
 *
 * 详细逻辑 / Detailed Logic：
 *   - 小于 1 分钟 → "刚刚"；
 *   - 小于 60 分钟 → "N 分钟前"；
 *   - 小于 24 小时 → "N 小时前"；
 *   - 超过 24 小时 → 显示本地化日期。
 *
 * @param ts - 毫秒时间戳 / Millisecond timestamp
 * @returns 人类可读的相对时间字符串 / Human-readable relative time string
 */
export function formatRelativeTime(ts: number): string {
  // 计算当前时间与目标时间的差值（毫秒）/ Calculate difference between now and target time (ms)
  const diff = Date.now() - ts;
  // 转换为分钟数（向下取整）/ Convert to minutes (floor)
  const min = Math.floor(diff / 60000);
  // 不足 1 分钟显示"刚刚" / Less than 1 minute shows "just now"
  if (min < 1) return '刚刚';
  // 不足 1 小时显示分钟数 / Less than 1 hour shows minutes
  if (min < 60) return `${min} 分钟前`;
  // 转换为小时数 / Convert to hours
  const hour = Math.floor(min / 60);
  // 不足 24 小时显示小时数 / Less than 24 hours shows hours
  if (hour < 24) return `${hour} 小时前`;
  // 超过 24 小时显示本地化日期（如 2024/1/15）/ Over 24 hours shows localized date
  return new Date(ts).toLocaleDateString();
}
