/**
 * levelColor 等级着色工具单元测试。
 * Unit tests for the levelColor level-coloring utility.
 *
 * 覆盖：
 *   1. 归一化位置计算（最低/最高/中间/未知等级）；
 *   2. chip / solid 类名随等级位置渐变（绿 → 红）；
 *   3. 跨标准一致性（不同等级体系按相对位置着色）。
 */
import { describe, it, expect } from 'vitest';
import { levelRatio, levelChipClass, levelSolidClass } from '../levelColor';
import type { StandardLevel } from '@/types/api';

/** 构造等级体系辅助函数 / helper to build a level system */
const levels = (ids: string[]): StandardLevel[] =>
  ids.map((id, i) => ({ id, name: id, rank: i + 1 }));

describe('levelRatio 归一化位置', () => {
  it('最低等级返回 0，最高等级返回 1', () => {
    const sys = levels(['L1', 'L2', 'L3', 'L4', 'L5']);
    expect(levelRatio('L1', sys)).toBe(0);
    expect(levelRatio('L5', sys)).toBe(1);
  });

  it('中间等级返回相对位置', () => {
    const sys = levels(['L1', 'L2', 'L3', 'L4', 'L5']);
    expect(levelRatio('L3', sys)).toBeCloseTo(0.5);
  });

  it('未知等级回退到 0.5', () => {
    const sys = levels(['L1', 'L2', 'L3']);
    expect(levelRatio('XX', sys)).toBe(0.5);
  });

  it('空体系或单等级体系安全处理', () => {
    expect(levelRatio('L1', [])).toBe(0.5);
    expect(levelRatio('L1', levels(['L1']))).toBe(0);
  });
});

describe('levelChipClass / levelSolidClass 渐变色阶', () => {
  it('最低等级偏绿，最高等级偏红', () => {
    const sys = levels(['G1', 'G2', 'G3', 'G4']);
    expect(levelChipClass('G1', sys)).toContain('emerald');
    expect(levelChipClass('G4', sys)).toContain('red');
    expect(levelSolidClass('G4', sys)).toContain('red');
  });

  it('跨标准按相对位置着色（四川 L1 与广东 G1 同为最低档）', () => {
    const sc = levels(['L1', 'L2', 'L3', 'L4', 'L5']);
    const gd = levels(['G1', 'G2', 'G3', 'G4']);
    // 两者最低等级应使用同一档位颜色 / both lowest levels share the same step color
    expect(levelChipClass('L1', sc)).toBe(levelChipClass('G1', gd));
  });
});
