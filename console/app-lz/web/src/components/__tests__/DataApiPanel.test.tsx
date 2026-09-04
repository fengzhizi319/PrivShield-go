import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import React from 'react';
import { DataApiPanel } from '../DataApiPanel';
import { I18nProvider } from '../../i18n';
import { DataApiDef, DataApiSessionResponse } from '../../types/api';

const mockApis: DataApiDef[] = [
  {
    id: 1,
    seq: 1,
    name: '柳州市医保结算数据查询 API',
    description: '提供医保结算流水号、就诊医院、诊断编码等字段的合规脱敏查询',
    datasource_id: 'ds_yibao',
    category: '医疗健康',
    fields: ['insurance_settlement_id', 'person_id', 'gender', 'icd10_code'],
    status: 'active',
  },
  {
    id: 2,
    seq: 2,
    name: '柳州市康养中心长者健康档案 API',
    description: '提供康养中心入住长者的体征体检与病历信息的合规脱敏查询',
    datasource_id: 'ds_kangyang',
    category: '康养服务',
    fields: ['elder_id', 'name', 'age', 'gender', 'chronic_conditions'],
    status: 'active',
  },
  {
    id: 3,
    seq: 3,
    name: '预留数据 API #1',
    description: '预留给未来业务系统接入',
    category: '预留',
    fields: [],
    status: 'reserved',
  },
];

const mockSessionSuccess: DataApiSessionResponse = {
  session_id: 'session-api1_yibao-12345678',
  api_id: 1,
  api_name: '柳州市医保结算数据查询 API',
  status: 'completed',
  raw_records: [
    { person_id: '450201198501011234', name: '李某某', diagnosis: '原发性高血压' },
  ],
  sanitized_data: [
    { person_id: '450201********1234', name: '李**', diagnosis: '原发性高血压' },
  ],
  stages: [
    { name: 'ingest', title: '会话请求接入与校验', status: 'success', duration_ms: 1, detail: 'API 校验通过' },
    { name: 'fetch', title: '数据源原始数据拉取', status: 'success', duration_ms: 5, detail: '拉取 1 条记录' },
    { name: 'classify_desensitize', title: '三层漏斗评级与隐私脱敏治理', status: 'success', duration_ms: 18, detail: '识别 3 个字段并完成脱敏' },
    { name: 'return', title: '脱敏结果装配与交付', status: 'success', duration_ms: 1, detail: '装配完成' },
    { name: 'audit', title: '不可篡改审计存证', status: 'success', duration_ms: 3, detail: 'SHA-256 存证已写入' },
  ],
  audit_entry_id: 'audit-entry-889900',
  total_duration_ms: 28,
};

describe('DataApiPanel Component', () => {
  it('renders 5 merged pipeline stages in flow diagram', () => {
    const onInvoke = vi.fn();

    render(
      <I18nProvider>
        <DataApiPanel
          apis={mockApis}
          onInvoke={onInvoke}
          loading={false}
        />
      </I18nProvider>
    );

    expect(screen.getByText('预设数据 API 全链路会话测试')).toBeInTheDocument();
    expect(screen.getByText(/5 阶段流水线/)).toBeInTheDocument();

    // Verify the 5 merged stages are present
    expect(screen.getByText(/1\. Ingest/)).toBeInTheDocument();
    expect(screen.getByText(/2\. Fetch/)).toBeInTheDocument();
    expect(screen.getByText(/3\. Classify & Desensitize/)).toBeInTheDocument();
    expect(screen.getByText(/4\. Return/)).toBeInTheDocument();
    expect(screen.getByText(/5\. Audit/)).toBeInTheDocument();
  });

  it('triggers onInvoke callback and displays session results with merged classify_desensitize stage', async () => {
    const onInvoke = vi.fn().mockResolvedValue(mockSessionSuccess);

    render(
      <I18nProvider>
        <DataApiPanel
          apis={mockApis}
          onInvoke={onInvoke}
          loading={false}
        />
      </I18nProvider>
    );

    const input = screen.getByPlaceholderText('点击选择或输入身份证号');
    fireEvent.change(input, { target: { value: '450201198501011234' } });

    const invokeButtons = screen.getAllByText('申请数据 (触发全链路)');
    expect(invokeButtons.length).toBeGreaterThan(0);
    fireEvent.click(invokeButtons[0]);

    await waitFor(() => {
      expect(onInvoke).toHaveBeenCalledWith(1, '450201198501011234');
    });

    await waitFor(() => {
      expect(screen.getByText(/会话结果/)).toBeInTheDocument();
      expect(screen.getByText('三层漏斗评级与隐私脱敏治理')).toBeInTheDocument();
      expect(screen.getByText('audit-entry-889900')).toBeInTheDocument();
    });
  });
});
