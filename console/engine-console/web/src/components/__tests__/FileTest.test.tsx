/**
 * FileTest validateFile 单元测试：验证客户端文件预校验逻辑。
 * 并补充渲染级 i18n 验证：组件文案随语言切换。
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { validateFile, MAX_UPLOAD_BYTES, ACCEPTED_EXTS } from '../FileTest';
import FileTest from '../FileTest';
import { I18nProvider } from '@/i18n';

// 模拟 Icon 组件（避免引入完整图标库）
vi.mock('@/components/icons', () => ({
  Icon: ({ name }: { name: string }) => <span data-testid={`icon-${name}`} />,
}));

/** 构造指定名称和大小的 File 对象。 */
function makeFile(name: string, sizeBytes: number): File {
  // 用稀疏内容模拟大小（jsdom 不关心实际内容）
  const content = sizeBytes > 0 ? 'x'.repeat(Math.min(sizeBytes, 1024)) : '';
  const file = new File([content], name, { type: 'text/csv' });
  // File.size 由 content 长度决定，需 mock 大文件场景
  if (sizeBytes > 1024) {
    Object.defineProperty(file, 'size', { value: sizeBytes });
  }
  return file;
}

describe('validateFile', () => {
  it('合法 .csv 文件通过', () => {
    const f = makeFile('data.csv', 1024);
    expect(validateFile(f)).toBeNull();
  });

  it('合法 .json 文件通过', () => {
    const f = makeFile('records.json', 2048);
    expect(validateFile(f)).toBeNull();
  });

  it('大写扩展名同样通过（大小写不敏感）', () => {
    const f = makeFile('DATA.CSV', 512);
    expect(validateFile(f)).toBeNull();
  });

  it('不支持的扩展名返回错误', () => {
    const f = makeFile('image.png', 1024);
    expect(validateFile(f)).toEqual({ code: 'unsupported_ext' });
  });

  it('无扩展名文件返回错误', () => {
    const f = makeFile('noext', 100);
    expect(validateFile(f)).toEqual({ code: 'unsupported_ext' });
  });

  it('超过大小上限返回错误', () => {
    const f = makeFile('big.csv', MAX_UPLOAD_BYTES + 1);
    const result = validateFile(f);
    expect(result?.code).toBe('too_large');
    expect(result?.limitMb).toBe(10);
    expect(result?.sizeMb).toBeDefined();
  });

  it('恰好等于上限时通过', () => {
    const f = makeFile('exact.csv', MAX_UPLOAD_BYTES);
    expect(validateFile(f)).toBeNull();
  });

  it('ACCEPTED_EXTS 包含 csv 和 json', () => {
    expect(ACCEPTED_EXTS).toContain('.csv');
    expect(ACCEPTED_EXTS).toContain('.json');
  });
});

describe('FileTest i18n 渲染', () => {
  afterEach(() => {
    localStorage.removeItem('console-lang');
  });

  it('默认中文渲染标题与操作选项', () => {
    render(
      <I18nProvider>
        <FileTest />
      </I18nProvider>,
    );
    expect(screen.getByText('数据文件隐私处理')).toBeInTheDocument();
    // 操作下拉默认选中脱敏，选项文案为中文
    expect(screen.getByRole('option', { name: '数据脱敏' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'K-匿名' })).toBeInTheDocument();
  });

  it('英文语言下渲染英文文案', () => {
    localStorage.setItem('console-lang', 'en');
    render(
      <I18nProvider>
        <FileTest />
      </I18nProvider>,
    );
    expect(screen.getByText('Data File Privacy Processing')).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Data Masking' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'K-Anonymity' })).toBeInTheDocument();
  });
});
