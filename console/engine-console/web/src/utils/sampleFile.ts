/**
 * 示例测试文件生成器 / Sample Test File Generator
 *
 * 前端“数据文件隐私处理”视图需要用户上传 CSV/JSON 文件，
 * 本模块在浏览器内预生成一份覆盖全部操作场景的示例数据文件，
 * 用户无需手工准备数据即可一键体验脱敏 / K-匿名 / 分类：
 * The frontend "Data File Privacy Processing" view requires users to upload CSV/JSON files,
 * this module pre-generates a sample data file covering all operation scenarios in the browser,
 * allowing users to experience masking / K-anonymity / classification without manual data preparation:
 *   - 含 email / phone 列 → 满足“数据脱敏”默认脱敏列（email, phone）；
 *     Contains email / phone columns → satisfies "masking" default columns (email, phone);
 *   - 含 age / zip / gender 列且行数充足 → 满足“K-匿名”默认准标识符（k=2）；
 *     Contains age / zip / gender columns with sufficient rows → satisfies "K-anonymity" default QI (k=2);
 *   - 含 name / salary / city 等常见敏感列 → 便于“数据分类”识别敏感等级。
 *     Contains name / salary / city common sensitive columns → facilitates "classification" sensitivity level identification.
 *
 * 生成的内容可直接构造为内存中的 ``File`` 对象填入上传控件，
 * 也可触发浏览器下载为本地文件，两种方式均不依赖后端。
 * Generated content can be directly constructed as in-memory ``File`` object for upload control,
 * or trigger browser download to local file, both methods are backend-independent.
 */

/** 示例数据列名（顺序即 CSV 表头顺序）/ Sample data column names (order = CSV header order) */
export const SAMPLE_COLUMNS = [
  'name',
  'email',
  'phone',
  'age',
  'zip',
  'gender',
  'salary',
  'city',
] as const;

/** 示例记录类型：列名到字符串值的映射 / Sample record type: column name to string value mapping */
export type SampleRecord = Record<(typeof SAMPLE_COLUMNS)[number], string>;

/**
 * 示例数据（16 行）。
 *
 * 设计要点：
 *   - email / phone 为格式规范的 PII，脱敏后可直观看到掩码效果；
 *   - age / zip / gender 组合多样，保证 K-匿名（k=2）有足够等价类可泛化；
 *   - salary 为数值型敏感字段，city 为低基数维度，便于分类与匿名对比。
 */
export const SAMPLE_RECORDS: SampleRecord[] = [
  { name: '张伟', email: 'zhangwei@example.com', phone: '13800001111', age: '28', zip: '100081', gender: 'male', salary: '15000', city: '北京' },
  { name: '李娜', email: 'lina@example.com', phone: '13900002222', age: '34', zip: '100082', gender: 'female', salary: '18000', city: '北京' },
  { name: '王强', email: 'wangqiang@example.com', phone: '13700003333', age: '41', zip: '200040', gender: 'male', salary: '22000', city: '上海' },
  { name: '刘洋', email: 'liuyang@example.com', phone: '13600004444', age: '29', zip: '200041', gender: 'female', salary: '16000', city: '上海' },
  { name: '陈杰', email: 'chenjie@example.com', phone: '13500005555', age: '38', zip: '510000', gender: 'male', salary: '20000', city: '广州' },
  { name: '杨敏', email: 'yangmin@example.com', phone: '13400006666', age: '45', zip: '510001', gender: 'female', salary: '25000', city: '广州' },
  { name: '赵磊', email: 'zhaolei@example.com', phone: '13300007777', age: '31', zip: '310000', gender: 'male', salary: '17000', city: '杭州' },
  { name: '黄丽', email: 'huangli@example.com', phone: '13200008888', age: '27', zip: '310001', gender: 'female', salary: '14000', city: '杭州' },
  { name: '周涛', email: 'zhoutao@example.com', phone: '13100009999', age: '52', zip: '610000', gender: 'male', salary: '28000', city: '成都' },
  { name: '吴芳', email: 'wufang@example.com', phone: '13000001010', age: '48', zip: '610001', gender: 'female', salary: '26000', city: '成都' },
  { name: '徐刚', email: 'xugang@example.com', phone: '15800001212', age: '36', zip: '430000', gender: 'male', salary: '19000', city: '长沙' },
  { name: '孙悦', email: 'sunyue@example.com', phone: '15900001313', age: '33', zip: '430001', gender: 'female', salary: '17500', city: '长沙' },
  { name: '马超', email: 'machao@example.com', phone: '15700001414', age: '26', zip: '810000', gender: 'male', salary: '13000', city: '深圳' },
  { name: '朱婷', email: 'zhuting@example.com', phone: '15600001515', age: '30', zip: '810001', gender: 'female', salary: '16500', city: '深圳' },
  { name: '胡军', email: 'hujun@example.com', phone: '15500001616', age: '44', zip: '250000', gender: 'male', salary: '23000', city: '南京' },
  { name: '郭静', email: 'guojing@example.com', phone: '15300001717', age: '39', zip: '250001', gender: 'female', salary: '21000', city: '南京' },
];

/** 支持的示例文件格式 / Supported sample file formats */
export type SampleFormat = 'csv' | 'json';

/** 各格式对应的 MIME 类型与默认文件名 / MIME type and default filename for each format */
const FORMAT_META: Record<SampleFormat, { mime: string; filename: string }> = {
  csv: { mime: 'text/csv', filename: 'privacy-sample.csv' },
  json: { mime: 'application/json', filename: 'privacy-sample.json' },
};

/**
 * 生成 CSV 文本内容 / Generate CSV text content
 *
 * 本示例数据不含逗号 / 引号 / 换行等特殊字符，直接以逗号拼接即可，
 * 无需引入 CSV 转义逻辑。
 * Sample data contains no commas / quotes / newlines, direct comma concatenation suffices,
 * no CSV escaping logic needed.
 */
export function buildSampleCsv(): string {
  const header = SAMPLE_COLUMNS.join(',');
  const rows = SAMPLE_RECORDS.map((r) => SAMPLE_COLUMNS.map((c) => r[c]).join(','));
  return [header, ...rows].join('\n') + '\n';
}

/** 生成 JSON 文本内容（记录对象数组，缩进 2 空格）/ Generate JSON text content (record object array, 2-space indent) */
export function buildSampleJson(): string {
  return JSON.stringify(SAMPLE_RECORDS, null, 2) + '\n';
}

/** 按格式生成示例文件的文本内容 / Generate sample file text content by format */
export function buildSampleContent(format: SampleFormat): string {
  return format === 'csv' ? buildSampleCsv() : buildSampleJson();
}

/**
 * 把示例内容构造为内存中的 ``File`` 对象 / Construct sample content as in-memory ``File`` object
 *
 * 返回的 File 可直接交给上传逻辑（与用户从磁盘选择的文件等价），
 * 从而免去手工准备测试文件的步骤。
 * Returned File can be directly passed to upload logic (equivalent to user-selected file from disk),
 * eliminating the need to manually prepare test files.
 */
export function createSampleFile(format: SampleFormat): File {
  const { mime, filename } = FORMAT_META[format];
  return new File([buildSampleContent(format)], filename, { type: mime });
}

/**
 * 触发浏览器下载示例文件到本地磁盘 / Trigger browser download of sample file to local disk
 *
 * 供希望把测试文件保存下来反复使用的场景；
 * 与 :func:`createSampleFile` 内容完全一致。
 * For scenarios where users want to save test files for repeated use;
 * Content is identical to :func:`createSampleFile`.
 */
export function downloadSampleFile(format: SampleFormat): void {
  const { mime, filename } = FORMAT_META[format];
  const blob = new Blob([buildSampleContent(format)], { type: mime });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  // P42 fix + P48 enhancement: 延迟释放 Blob URL 并清理 DOM 元素。
  // Delayed Blob URL revocation and DOM cleanup.
  // 10 秒后释放足以覆盖所有浏览器的下载启动延迟。
  // 10s delay covers all browsers' download startup latency.
  setTimeout(() => {
    URL.revokeObjectURL(url);
    a.remove();
  }, 10_000);
}
