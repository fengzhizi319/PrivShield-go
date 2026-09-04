/**
 * 敏感等级着色工具：把分类分级等级映射为「绿 → 黄 → 红」渐变色阶。
 * Level coloring utility: maps classification levels to a green → yellow → red gradient.
 *
 * 设计要点 / Design Notes：
 *   - 不同标准的等级体系各异（四川 L1~L5、金融 C1~C4、广东 G1~G4），
 *     无法按等级 ID 硬编码颜色；改为依据等级在体系中的相对位置（rank 归一化）
 *     映射到固定色阶，低敏感偏绿、高敏感偏红，跨标准视觉语义一致。
 *     Standards differ in level systems (Sichuan L1~L5, Financial C1~C4, GD G1~G4),
 *     so colors cannot be hard-coded by level ID; instead we map the relative
 *     position (normalized rank) within the system onto a fixed palette, so that
 *     low-sensitivity leans green and high-sensitivity leans red, keeping a
 *     consistent visual semantics across standards.
 *   - 色阶使用 Tailwind 字面量类名（非动态拼接），保证 JIT 编译器可扫描生成。
 *     Palette uses Tailwind literal class names (no dynamic concatenation) so the
 *     JIT compiler can scan and generate them.
 */
import type { StandardLevel } from '@/types/api';

/**
 * 固定色阶（低敏感 → 高敏感）。每个元素为一组字面量 Tailwind 类：
 * chip 用于等级徽章（背景 + 文字），solid 用于大号最终等级展示。
 * Fixed palette (low → high sensitivity). Each entry is a set of literal Tailwind
 * classes: `chip` for level badges (bg + text), `solid` for the large final-level display.
 */
const PALETTE: { chip: string; solid: string }[] = [
  // 档位 0：最低敏感（绿）/ Step 0: lowest sensitivity (green)
  { chip: 'bg-emerald-100 text-emerald-700', solid: 'bg-emerald-500 text-white' },
  // 档位 1：较低敏感（青绿）/ Step 1: lower sensitivity (teal)
  { chip: 'bg-teal-100 text-teal-700', solid: 'bg-teal-500 text-white' },
  // 档位 2：中低敏感（黄）/ Step 2: medium-low sensitivity (amber)
  { chip: 'bg-amber-100 text-amber-700', solid: 'bg-amber-500 text-white' },
  // 档位 3：中高敏感（橙）/ Step 3: medium-high sensitivity (orange)
  { chip: 'bg-orange-100 text-orange-700', solid: 'bg-orange-500 text-white' },
  // 档位 4：高敏感（红）/ Step 4: high sensitivity (red)
  { chip: 'bg-red-100 text-red-700', solid: 'bg-red-600 text-white' },
];

/**
 * 把归一化位置 [0,1] 映射到色阶档位索引。
 * Map a normalized position [0,1] to a palette step index.
 *
 * @param ratio - 归一化位置（0 = 最低敏感，1 = 最高敏感）/ normalized position
 * @returns 色阶档位索引 / palette step index
 */
function ratioToStep(ratio: number): number {
  //  clamp 到 [0,1] 后按比例取档位，最后一档留给 ratio=1 / clamp then scale, last step reserved for ratio=1
  const clamped = Math.min(1, Math.max(0, ratio));
  const idx = Math.round(clamped * (PALETTE.length - 1));
  return Math.min(PALETTE.length - 1, Math.max(0, idx));
}

/**
 * 计算某等级在其等级体系中的归一化位置。
 * Compute the normalized position of a level within its level system.
 *
 * 体系只有一个等级时返回 0（视为最低敏感）。
 * Returns 0 when the system has only one level (treated as lowest sensitivity).
 *
 * @param levelId - 目标等级 ID / target level ID
 * @param levels - 完整等级体系（含 rank）/ full level system (with rank)
 * @returns 归一化位置 [0,1]；等级不在体系中时回退到 0.5 / normalized position; falls back to 0.5 if level not found
 */
export function levelRatio(levelId: string, levels: StandardLevel[]): number {
  if (!levels || levels.length === 0) return 0.5;
  // 按 rank 排序得到稳定顺序 / sort by rank for a stable order
  const sorted = [...levels].sort((a, b) => a.rank - b.rank);
  const idx = sorted.findIndex((lv) => lv.id === levelId);
  if (idx < 0) return 0.5; // 未知等级取中间色 / unknown level takes the middle color
  if (sorted.length === 1) return 0;
  return idx / (sorted.length - 1);
}

/**
 * 获取等级徽章（chip）的 Tailwind 类名。
 * Get Tailwind classes for a level badge (chip).
 *
 * @param levelId - 等级 ID / level ID
 * @param levels - 等级体系 / level system
 * @returns 字面量 Tailwind 类名串 / literal Tailwind class string
 */
export function levelChipClass(levelId: string, levels: StandardLevel[]): string {
  return PALETTE[ratioToStep(levelRatio(levelId, levels))].chip;
}

/**
 * 获取大号最终等级展示（solid）的 Tailwind 类名。
 * Get Tailwind classes for the large final-level display (solid).
 *
 * @param levelId - 等级 ID / level ID
 * @param levels - 等级体系 / level system
 * @returns 字面量 Tailwind 类名串 / literal Tailwind class string
 */
export function levelSolidClass(levelId: string, levels: StandardLevel[]): string {
  return PALETTE[ratioToStep(levelRatio(levelId, levels))].solid;
}
