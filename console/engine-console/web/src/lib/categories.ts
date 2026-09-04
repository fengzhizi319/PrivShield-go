/**
 * 分类元数据模块：定义接口分类的展示属性（图标、配色、描述、排序）。
 * Category metadata module: defines display attributes for API categories (icon, color, description, order).
 *
 * 本模块为侧边栏（Sidebar）和总览页（Overview）提供统一的分类视觉配置，
 * This module provides unified category visual config for Sidebar and Overview pages,
 * 确保新增分类时只需在此处添加一条记录即可全局生效。
 * ensuring a new category only needs one entry here to take effect globally.
 */
import type { IconName } from '@/components/icons'; // 图标名称类型 / Icon name type

/**
 * 分类元数据接口：每个分类的视觉与描述属性。
 * Category metadata interface: visual and descriptive attributes per category.
 *
 * 配色使用 Tailwind 字面量类名（非动态拼接），确保 JIT 编译器能正确扫描生成。
 * Colors use Tailwind literal class names (not dynamic), ensuring JIT compiler can scan and generate.
 */
export interface CategoryMeta {
  icon: IconName;   // 分类图标名称 / Category icon name
  /** 图标底色 + 前景色（用于侧边栏与概览卡片）/ Icon bg + fg color (for sidebar & overview cards) */
  chip: string;
  /** 概览卡片顶部渐变色条 / Overview card top gradient bar */
  accent: string;
  desc: string;     // 中文功能描述 / Chinese functional description
}

/**
 * 分类展示顺序常量：定义侧边栏与总览页中分类的固定排列顺序。
 * Category display order constant: defines fixed ordering in sidebar and overview.
 * 未在此列表中的分类会被追加到末尾（见 orderCategories 函数）。
 * Categories not in this list are appended at the end (see orderCategories function).
 */
export const CATEGORY_ORDER = [
  'Health',               // 健康检查（最基础，排首位）/ Health check (most basic, first)
  'Masking',              // 数据脱敏 / Data masking
  'Hash',                 // HMAC 哈希 / HMAC hash
  'DP',                   // 差分隐私 / Differential privacy
  'LDP',                  // 本地差分隐私 / Local differential privacy
  'K-Anonymity',          // K-匿名 / K-anonymity
  'Query Obfuscation',    // 查询混淆 / Query obfuscation
  'DynamicClassification',// 动态分类分级 / Dynamic classification
  'Budget',               // 隐私预算 / Privacy budget
  'Profile',              // 参数推荐 / Parameter recommendation
] as const;

/**
 * 分类元数据映射表：分类名称 → 视觉属性。
 * Category metadata map: category name → visual attributes.
 * 每个分类定义独立的图标、配色方案和中文描述。
 * Each category defines its own icon, color scheme, and Chinese description.
 */
export const CATEGORY_META: Record<string, CategoryMeta> = {
  Health: {
    icon: 'activity',
    chip: 'bg-emerald-50 text-emerald-600',
    accent: 'from-emerald-400 to-teal-500',
    desc: '健康检查与就绪探针',
  },
  Masking: {
    icon: 'eye-off',
    chip: 'bg-indigo-50 text-indigo-600',
    accent: 'from-indigo-400 to-violet-500',
    desc: '字段 / 记录 / 批量数据脱敏',
  },
  Hash: {
    icon: 'hash',
    chip: 'bg-slate-100 text-slate-600',
    accent: 'from-slate-400 to-slate-600',
    desc: 'HMAC 哈希',
  },
  DP: {
    icon: 'bar-chart',
    chip: 'bg-sky-50 text-sky-600',
    accent: 'from-sky-400 to-blue-500',
    desc: '差分隐私：count / sum / mean / 直方图等',
  },
  LDP: {
    icon: 'shuffle',
    chip: 'bg-cyan-50 text-cyan-600',
    accent: 'from-cyan-400 to-sky-500',
    desc: '本地差分隐私扰动与估计',
  },
  'K-Anonymity': {
    icon: 'users',
    chip: 'bg-violet-50 text-violet-600',
    accent: 'from-violet-400 to-purple-500',
    desc: 'K-匿名泛化',
  },
  'Query Obfuscation': {
    icon: 'help',
    chip: 'bg-amber-50 text-amber-600',
    accent: 'from-amber-400 to-orange-500',
    desc: '查询混淆 / 假查询注入',
  },
  DynamicClassification: {
    icon: 'sparkles',
    chip: 'bg-purple-50 text-purple-600',
    accent: 'from-purple-400 to-indigo-500',
    desc: '声明式通用动态分类分级与自动配置',
  },
  Budget: {
    icon: 'wallet',
    chip: 'bg-lime-50 text-lime-600',
    accent: 'from-lime-400 to-green-500',
    desc: '隐私预算查询',
  },
  Profile: {
    icon: 'sliders',
    chip: 'bg-fuchsia-50 text-fuchsia-600',
    accent: 'from-fuchsia-400 to-pink-500',
    desc: '隐私参数推荐',
  },
};


/**
 * 兜底分类配置：当后端返回未知分类名时使用。
 * Fallback category config: used when backend returns an unknown category name.
 * 使用灰色中性配色，避免视觉突兀。
 * Uses neutral gray colors to avoid visual disruption.
 */
export const FALLBACK_META: CategoryMeta = {
  icon: 'inbox',                        // 通用收件箱图标 / Generic inbox icon
  chip: 'bg-gray-100 text-gray-600',    // 灰色底 + 灰色字 / Gray bg + gray text
  accent: 'from-gray-400 to-gray-500',  // 灰色渐变条 / Gray gradient bar
  desc: '其他接口',                      // 默认描述 / Default description
};

/**
 * 获取指定分类的元数据，未注册分类返回兜底配置。
 * Get metadata for a given category; returns fallback config for unregistered categories.
 * @param name - 分类名称（如 "Masking"）/ Category name (e.g. "Masking")
 * @returns 分类元数据对象 / Category metadata object
 */
export function categoryMeta(name: string): CategoryMeta {
  return CATEGORY_META[name] ?? FALLBACK_META; // 查找映射表，未命中则兜底 / Lookup map, fallback if miss
}

/**
 * 按预定义顺序排列分类，未知分类追加在末尾。
 * Sort categories by predefined order; unknown categories appended at the end.
 *
 * 执行逻辑 / Execution logic:
 *   1. 从 CATEGORY_ORDER 中筛选出当前存在的分类（保持预定义顺序）
 *      Filter CATEGORY_ORDER to only present categories (preserving predefined order)
 *   2. 将不在预定义列表中的分类追加到末尾
 *      Append categories not in the predefined list to the end
 *
 * @param present - 当前后端返回的所有分类名数组 / Array of all category names from backend
 * @returns 排序后的分类名数组 / Sorted array of category names
 */
export function orderCategories(present: string[]): string[] {
  // 第一步：保留预定义顺序中存在的分类 / Step 1: keep present categories in predefined order
  const ordered = CATEGORY_ORDER.filter((c) => present.includes(c)) as unknown as string[];
  // 第二步：追加未知分类（后端新增但前端未预见的）/ Step 2: append unknown categories
  for (const c of present) {
    if (!ordered.includes(c)) ordered.push(c); // 去重追加 / Deduplicated append
  }
  return ordered; // 返回最终排序结果 / Return final sorted result
}
