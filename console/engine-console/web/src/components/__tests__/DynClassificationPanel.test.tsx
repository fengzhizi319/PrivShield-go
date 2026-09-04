/**
 * DynClassificationPanel 全局标准切换器测试。
 *
 * 覆盖：
 *   1. 挂载时拉取标准列表并渲染三个标准选项（四川/金融/广东）；
 *   2. 切换标准后展示对应等级体系徽章与默认等级；
 *   3. 切换后字段级/记录级评估请求携带新标准 ID（全链路切换）；
 *   4. 未选择标准时请求不携带 standard 字段；
 *   5. 标准列表拉取失败时面板降级可用。
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import DynClassificationPanel from '../DynClassificationPanel';
import * as apiClient from '@/api/client';
import { I18nProvider } from '@/i18n';

// Mock API 客户端模块（面板仅使用 proxyRequest 与 fetchStandards）
vi.mock('@/api/client', () => ({
  proxyRequest: vi.fn(),
  fetchStandards: vi.fn(),
}));

/**
 * 面板渲染助手：用 I18nProvider 包裹（默认 zh），
 * 使面板内部的 t() 能解析为中文文案（否则无 Provider 时 t() 原样返回 key）。
 * Panel render helper wrapped with I18nProvider (default zh) so that t()
 * resolves to Chinese text (without a Provider, t() returns the key as-is).
 */
function renderPanel() {
  return render(
    <I18nProvider>
      <DynClassificationPanel />
    </I18nProvider>,
  );
}

/**
 * 定位顶部标准切换器：domain 输入框因 datalist 同样具备 combobox 语义，
 * 故需用 aria-label（中英）精确区分标准的 select。
 * Locate the top standard switcher: the domain input also has combobox
 * semantics due to its datalist, so we disambiguate via aria-label (zh/en).
 */
function standardCombobox() {
  return screen.getByRole('combobox', { name: /当前标准|Current Standard/ });
}

/** 三标准详情 mock 数据（与后端 GET /v1/dynclassification/standards 结构一致）。 */
const mockStandardsResponse = {
  standards: ['gd_health', 'jrt0197', 'sc_health_db51'],
  details: [
    {
      standard_id: 'gd_health',
      description: '广东省健康医疗数据安全分类分级管理技术规范',
      taxonomy: 'gd_health',
      domains: ['gd_health'],
      default_level: 'G2',
      levels: [
        { id: 'G1', name: '第1级（低敏感）', rank: 1 },
        { id: 'G2', name: '第2级（较低敏感）', rank: 2 },
        { id: 'G3', name: '第3级（敏感）', rank: 3 },
        { id: 'G4', name: '第4级（高敏感）', rank: 4 },
      ],
    },
    {
      standard_id: 'jrt0197',
      description: '金融数据安全分级指南',
      taxonomy: 'jrt0197',
      domains: ['finance', 'general-pii'],
      default_level: 'C3',
      levels: [
        { id: 'C1', name: 'C1 级', rank: 1 },
        { id: 'C2', name: 'C2 级', rank: 2 },
        { id: 'C3', name: 'C3 级', rank: 3 },
        { id: 'C4', name: 'C4 级', rank: 4 },
      ],
    },
    {
      standard_id: 'sc_health_db51',
      description: '四川省健康医疗大数据应用指南',
      taxonomy: 'sc_health_db51',
      domains: ['general-pii', 'medical'],
      default_level: 'L3',
      levels: [
        { id: 'L1', name: '一级', rank: 1 },
        { id: 'L2', name: '二级', rank: 2 },
        { id: 'L3', name: '三级', rank: 3 },
        { id: 'L4', name: '四级', rank: 4 },
        { id: 'L5', name: '五级', rank: 5 },
      ],
    },
  ],
};

describe('DynClassificationPanel 全局标准切换器', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiClient.fetchStandards).mockResolvedValue(mockStandardsResponse as any);
  });

  it('挂载时拉取标准列表并渲染三个标准选项', async () => {
    renderPanel();

    await waitFor(() => expect(apiClient.fetchStandards).toHaveBeenCalledTimes(1));

    const select = standardCombobox();
    // 默认选项 + 三个标准选项
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(4);
    expect(options[0]).toHaveTextContent('默认（通用规则引擎）');
    expect(select).toHaveTextContent('sc_health_db51 — 四川省健康医疗大数据应用指南');
    expect(select).toHaveTextContent('gd_health — 广东省健康医疗数据安全分类分级管理技术规范');
    expect(select).toHaveTextContent('jrt0197 — 金融数据安全分级指南');
  });

  it('初始未选择标准时展示通用引擎提示', async () => {
    renderPanel();
    await waitFor(() =>
      expect(screen.getByText('使用通用规则引擎（未选择标准）')).toBeInTheDocument()
    );
  });

  it('切换到广东标准后展示 G1~G4 等级体系与默认等级', async () => {
    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    fireEvent.change(standardCombobox(), { target: { value: 'gd_health' } });

    // 描述徽章 + 等级 chips + 默认等级
    expect(screen.getByText('广东省健康医疗数据安全分类分级管理技术规范')).toBeInTheDocument();
    for (const lv of ['G1', 'G2', 'G3', 'G4']) {
      expect(screen.getByText(lv)).toBeInTheDocument();
    }
    expect(screen.getByText('默认等级: G2')).toBeInTheDocument();
    // 输入区提示同步更新
    expect(
      screen.getAllByText(/分类标准由顶部切换器控制：gd_health（广东省健康医疗数据安全分类分级管理技术规范）/)
        .length
    ).toBeGreaterThan(0);
  });

  it('切换到四川标准后展示 L1~L5 等级体系', async () => {
    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    fireEvent.change(standardCombobox(), { target: { value: 'sc_health_db51' } });

    for (const lv of ['L1', 'L2', 'L3', 'L4', 'L5']) {
      expect(screen.getByText(lv)).toBeInTheDocument();
    }
    expect(screen.getByText('默认等级: L3')).toBeInTheDocument();
  });

  it('字段评估请求携带当前选中的标准 ID', async () => {
    vi.mocked(apiClient.proxyRequest).mockResolvedValue({
      data: { fieldResult: { finalLevel: 'G4', confidence: 0.95 } },
    } as any);

    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    fireEvent.change(standardCombobox(), { target: { value: 'gd_health' } });
    fireEvent.click(screen.getByText('执行动态分类评估'));

    await waitFor(() => expect(apiClient.proxyRequest).toHaveBeenCalledTimes(1));
    const req = vi.mocked(apiClient.proxyRequest).mock.calls[0][0] as any;
    expect(req.method).toBe('POST');
    expect(req.path).toBe('/v1/dynclassification/eval');
    expect(req.body.standard).toBe('gd_health');
    expect(req.body.fieldName).toBe('mobile_phone');
  });

  it('切换为四川标准后记录级分类请求携带 sc_health_db51', async () => {
    vi.mocked(apiClient.proxyRequest).mockResolvedValue({
      data: { recordResult: { finalLevel: 'L3' } },
    } as any);

    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    fireEvent.change(standardCombobox(), { target: { value: 'sc_health_db51' } });
    fireEvent.click(screen.getByText('记录级分类 (Record)'));
    fireEvent.click(screen.getByText('执行记录级分类'));

    await waitFor(() => expect(apiClient.proxyRequest).toHaveBeenCalledTimes(1));
    const req = vi.mocked(apiClient.proxyRequest).mock.calls[0][0] as any;
    expect(req.path).toBe('/v1/dynclassification/eval_record');
    expect(req.body.standard).toBe('sc_health_db51');
    expect(req.body.record).toEqual({
      name: '张三',
      id_card: '110101199001011237',
      phone: '13800138000',
    });
  });

  it('未选择标准时评估请求不携带 standard 字段', async () => {
    vi.mocked(apiClient.proxyRequest).mockResolvedValue({
      data: { fieldResult: { finalLevel: 'L3' } },
    } as any);

    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    fireEvent.click(screen.getByText('执行动态分类评估'));

    await waitFor(() => expect(apiClient.proxyRequest).toHaveBeenCalledTimes(1));
    const req = vi.mocked(apiClient.proxyRequest).mock.calls[0][0] as any;
    expect(req.body).not.toHaveProperty('standard');
  });

  it('标准列表拉取失败时面板降级可用（仅默认选项）', async () => {
    vi.mocked(apiClient.fetchStandards).mockRejectedValue(new Error('网络错误'));

    renderPanel();

    await waitFor(() =>
      expect(screen.getByText('使用通用规则引擎（未选择标准）')).toBeInTheDocument()
    );
    // 仅默认选项
    expect(screen.getAllByRole('option')).toHaveLength(1);
    // 面板其余功能仍可操作
    expect(screen.getByText('执行动态分类评估')).toBeInTheDocument();
  });
});

/** 字段级分类完整响应 mock（与后端 ClassificationResponse 结构一致）。 */
const mockFieldResponse = {
  fieldResult: {
    fieldName: 'id_card',
    fieldValue: '110101199003072316',
    tags: [
      {
        level: 'G4',
        category: 'PERSONAL_ATTRIBUTE',
        confidence: 1.0,
        sourceEngine: 'RULE',
        ruleId: 'RULE_GD_HEALTH_IDCARD',
        domain: 'gd_health',
        standardId: 'gd_health',
        version: '1.0.0',
        needsHumanReview: false,
        isOverride: false,
        isDowngrade: false,
        matchTarget: 'field_value',
      },
    ],
    finalLevel: 'G4',
    confidence: 1.0,
    needsHumanReview: false,
    engineLayer: 'L1_RULE',
    reasoning: '命中规则: RULE_GD_HEALTH_IDCARD',
    suppressedTags: [],
  },
  auditInfo: {
    version: '1.0.0',
    domain: 'gd_health',
    standardId: 'gd_health',
    timestamp: '2026-08-02T15:49:57.618701+00:00',
    ruleSetVersion: '1.0.0',
    rulesEvaluated: 17,
    rulesHit: 2,
    durationMs: 14.131,
  },
};

describe('DynClassificationPanel 结构化结果展示', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiClient.fetchStandards).mockResolvedValue(mockStandardsResponse as any);
  });

  it('字段评估结果结构化展示：最终等级/推理说明/命中标签/审计信息', async () => {
    vi.mocked(apiClient.proxyRequest).mockResolvedValue({ data: mockFieldResponse } as any);

    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    // 选中广东标准（提供 G1~G4 等级体系供着色）/ select GD standard for level coloring
    fireEvent.change(standardCombobox(), { target: { value: 'gd_health' } });
    fireEvent.click(screen.getByText('执行动态分类评估'));

    // 最终等级徽章 + 引擎层 / final level badge + engine layer
    await waitFor(() => expect(screen.getAllByText('G4').length).toBeGreaterThan(0));
    expect(screen.getByText(/引擎层: L1_RULE/)).toBeInTheDocument();
    // 推理说明 / reasoning
    expect(screen.getAllByText(/命中规则: RULE_GD_HEALTH_IDCARD/).length).toBeGreaterThan(0);
    // 命中标签卡片：类别 + 规则 ID / tag card: category + rule ID
    expect(screen.getAllByText('PERSONAL_ATTRIBUTE').length).toBeGreaterThan(0);
    expect(screen.getAllByText('RULE_GD_HEALTH_IDCARD').length).toBeGreaterThan(0);
    // 审计信息：命中规则数 / audit: rules hit count
    expect(screen.getAllByText(/命中规则/).length).toBeGreaterThan(0);
    // 可折叠原始 JSON / collapsible raw JSON
    expect(screen.getByText(/查看原始 JSON/)).toBeInTheDocument();
  });

  it('记录级分类结果以逐字段表格展示', async () => {
    const recordResponse = {
      recordResult: {
        recordIndex: 0,
        fieldResults: {
          id_card: {
            fieldName: 'id_card',
            tags: [{ ruleId: 'RULE_PII_IDCARD' }],
            finalLevel: 'L3',
            confidence: 1.0,
            needsHumanReview: false,
            engineLayer: 'L1_RULE',
            reasoning: '',
            suppressedTags: [],
          },
          phone: {
            fieldName: 'phone',
            tags: [{ ruleId: 'RULE_PII_PHONE' }],
            finalLevel: 'L2',
            confidence: 0.9,
            needsHumanReview: false,
            engineLayer: 'L1_RULE',
            reasoning: '',
            suppressedTags: [],
          },
        },
        aggregatedTags: [],
        finalLevel: 'L3',
        confidence: 1.0,
        needsHumanReview: false,
      },
      auditInfo: {
        version: '1.0.0',
        domain: '',
        standardId: 'sc_health_db51',
        timestamp: '2026-08-02T15:49:57.618701+00:00',
        ruleSetVersion: '1.0.0',
        rulesEvaluated: 10,
        rulesHit: 2,
        durationMs: 8.5,
      },
    };
    vi.mocked(apiClient.proxyRequest).mockResolvedValue({ data: recordResponse } as any);

    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    fireEvent.change(standardCombobox(), { target: { value: 'sc_health_db51' } });
    fireEvent.click(screen.getByText('记录级分类 (Record)'));
    fireEvent.click(screen.getByText('执行记录级分类'));

    // 逐字段表格：字段名 + 命中规则 / per-field table: field names + hit rules
    await waitFor(() => expect(screen.getAllByText('id_card').length).toBeGreaterThan(0));
    expect(screen.getAllByText('phone').length).toBeGreaterThan(0);
    expect(screen.getAllByText('RULE_PII_IDCARD').length).toBeGreaterThan(0);
    expect(screen.getAllByText('RULE_PII_PHONE').length).toBeGreaterThan(0);
    // 字段数提示 / field count hint
    expect(screen.getByText(/字段数: 2/)).toBeInTheDocument();
  });
});

describe('DynClassificationPanel i18n 国际化', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiClient.fetchStandards).mockResolvedValue(mockStandardsResponse as any);
  });

  afterEach(() => {
    // 清理语言偏好，避免污染其他测试 / clean language preference to avoid leaking
    localStorage.removeItem('console-lang');
  });

  it('语言偏好为 en 时渲染英文文案', async () => {
    localStorage.setItem('console-lang', 'en');
    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    // 标题 / Tab / 按钮均为英文 / title, tab and button are English
    expect(screen.getByText(/Declarative Dynamic Classification/)).toBeInTheDocument();
    expect(screen.getByText('Field Evaluation (Eval)')).toBeInTheDocument();
    expect(screen.getByText('Run Dynamic Evaluation')).toBeInTheDocument();
    // 标准切换器默认选项为英文 / standard switcher default option in English
    expect(screen.getAllByRole('option')[0]).toHaveTextContent('Default (Generic Rule Engine)');
  });

  it('默认（zh）与英文文案互斥：中文环境不出现英文按钮', async () => {
    renderPanel();
    await waitFor(() => expect(standardCombobox()).toBeEnabled());

    expect(screen.getByText('执行动态分类评估')).toBeInTheDocument();
    expect(screen.queryByText('Run Dynamic Evaluation')).not.toBeInTheDocument();
  });
});
