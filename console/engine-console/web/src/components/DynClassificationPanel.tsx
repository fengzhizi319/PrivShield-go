/**
 * 声明式通用动态分类分级面板 / Declarative Universal Dynamic Classification Panel
 *
 * 提供动态分类分级引擎的完整测试界面，包含五个 Tab：
 * Provides a complete test interface for the dynamic classification engine, with five tabs:
 *
 *   1. 字段动态评估 (Eval)：输入字段名/值/领域，获取分类结果（标准由顶部全局切换器控制）；
 *      Field Dynamic Evaluation: input field name/value/domain, get classification result (standard via the global switcher);
 *   2. 记录级分类 (Record)：输入整条 JSON 记录，对每个字段做分类；
 *      Record-level Classification: input full JSON record, classify each field;
 *   3. 标准文档一键生成配置 (Auto Generate)：从规范文档自动提取分类规则；
 *      Standard Doc Auto-generate Config: auto-extract classification rules from spec docs;
 *   4. 标准/领域/算子目录 (Directory)：查询系统已注册的标准、领域、算子；
 *      Standards/Domains/Operators Directory: query registered standards, domains, operators;
 *   5. 规则校验 (Validate)：校验当前 YAML 配置的完整性与一致性。
 *      Rule Validation: validate completeness and consistency of current YAML config.
 *
 * 所有请求均通过 proxyRequest 转发到后端 /v1/dynclassification/* 接口。
 * All requests are forwarded to backend /v1/dynclassification/* endpoints via proxyRequest.
 */

/** 引入 React 状态 Hook / Import React state Hook */
import { useEffect, useState } from 'react';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入代理请求 API / Import proxy request API */
import { proxyRequest, fetchStandards } from '@/api/client';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';
/** 引入通用异步动作 Hook / Import generic async action Hook */
import { useAsyncAction } from '@/hooks/useAsyncAction';
/** 引入标准详情类型 / Import standard detail type */
import type {
  StandardDetail,
  StandardLevel,
  ClassificationResponse,
  FieldClassificationResult,
  SecurityTag,
  GenerateProfileResponse,
  ValidateResponse,
} from '@/types/api';
/** 引入等级着色工具 / Import level coloring utilities */
import { levelChipClass, levelSolidClass } from '@/lib/levelColor';

/**
 * 等级徽章：按等级在体系中的相对位置渐变着色（绿→黄→红）。
 * Level badge: gradient-colored (green→yellow→red) by the level's relative position in the system.
 *
 * @param levelId - 等级 ID（如 L3 / C4 / G2）/ level ID
 * @param levels - 当前标准的等级体系（为空时回退中性灰）/ level system of current standard
 */
function LevelBadge({ levelId, levels }: { levelId: string; levels: StandardLevel[] }) {
  // 无等级体系上下文时使用中性灰 / fall back to neutral gray without a level system context
  const color = levels.length > 0 ? levelChipClass(levelId, levels) : 'bg-gray-100 text-gray-600';
  return (
    <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${color}`}>{levelId}</span>
  );
}

/**
 * 命中标签卡片：结构化展示单条 SecurityTag 的关键信息。
 * Tag card: structured display of a single SecurityTag's key info.
 */
function TagCard({ tag, levels }: { tag: SecurityTag; levels: StandardLevel[] }) {
  const { t } = useI18n(); // 读取翻译函数 / read translation function
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-3 shadow-sm">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <LevelBadge levelId={tag.level} levels={levels} />
          <span className="text-sm font-semibold text-gray-800">{tag.category}</span>
        </div>
        <span className="text-xs font-medium text-gray-500">{Math.round(tag.confidence * 100)}%</span>
      </div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500">
        {tag.ruleId && <span>{t('dyn.result.tag.rule')} <code className="text-gray-700">{tag.ruleId}</code></span>}
        <span>{t('dyn.result.tag.engine')} {tag.sourceEngine}</span>
        <span>{t('dyn.result.tag.match')} {tag.matchTarget}</span>
        {tag.needsHumanReview && <span className="text-amber-600">{t('dyn.result.tag.needs_review')}</span>}
        {tag.isDowngrade && <span className="text-blue-600">{t('dyn.result.tag.downgrade')}</span>}
      </div>
    </div>
  );
}

/**
 * 审计信息条：展示分类请求的执行元数据（耗时/命中规则数/标准/引擎层）。
 * Audit bar: displays execution metadata of a classification request.
 */
function AuditBar({ resp }: { resp: ClassificationResponse }) {
  const { t } = useI18n(); // 读取翻译函数 / read translation function
  const audit = resp.auditInfo;
  if (!audit) return null;
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg bg-gray-100 px-3 py-2 text-xs text-gray-500">
      <span>{t('dyn.result.audit.duration')} <b className="text-gray-700">{audit.durationMs.toFixed(1)} ms</b></span>
      <span>{t('dyn.result.audit.rules_hit')} <b className="text-gray-700">{audit.rulesHit}</b> / {audit.rulesEvaluated}</span>
      {audit.standardId && <span>{t('dyn.result.audit.standard')} {audit.standardId}</span>}
      {audit.domain && <span>{t('dyn.result.audit.domain')} {audit.domain}</span>}
    </div>
  );
}

/**
 * 可折叠的原始 JSON 调试区：默认收起，供需要查看完整响应的用户展开。
 * Collapsible raw JSON debug area: collapsed by default, expandable for full response inspection.
 */
function RawJson({ data }: { data: unknown }) {
  const { t } = useI18n(); // 读取翻译函数 / read translation function
  return (
    <details className="overflow-hidden rounded-lg border border-gray-200 bg-gray-900 text-xs text-green-400">
      <summary className="cursor-pointer select-none bg-gray-800 px-4 py-2 text-gray-300">{t('dyn.result.raw_json')}</summary>
      <pre className="max-h-72 overflow-auto p-4">{JSON.stringify(data, null, 2)}</pre>
    </details>
  );
}

/**
 * 字段级评估结果结构化视图。
 * Structured view for field-level evaluation results.
 *
 * 组成：最终等级大徽章（渐变着色）+ 置信度/引擎层 + 推理说明 +
 * 命中标签卡片列表 + 审计信息条 + 可折叠原始 JSON。
 * Composition: large final-level badge (gradient) + confidence/engine layer + reasoning +
 * hit-tag card list + audit bar + collapsible raw JSON.
 */
function EvalResultView({ resp, levels }: { resp: ClassificationResponse; levels: StandardLevel[] }) {
  const { t } = useI18n(); // 读取翻译函数 / read translation function
  const field = resp.fieldResult;
  if (!field) {
    // 响应不含字段结果时回退到原始 JSON / fall back to raw JSON when no field result present
    return <RawJson data={resp} />;
  }
  return (
    <div className="space-y-4">
      {/* 最终等级 + 置信度 + 引擎层 / Final level + confidence + engine layer */}
      <div className="flex items-center justify-between rounded-lg bg-white p-4 shadow-sm">
        <div className="flex items-center gap-3">
          <span
            className={`flex h-14 w-14 items-center justify-center rounded-xl text-2xl font-black ${levelSolidClass(field.finalLevel, levels)}`}
          >
            {field.finalLevel}
          </span>
          <div>
            <span className="text-xs text-gray-500">{t('dyn.result.final_level')}</span>
            <div className="mt-0.5 text-xs text-gray-500">{t('dyn.result.engine_layer', field.engineLayer)}</div>
          </div>
        </div>
        <div className="text-right">
          <span className="text-xs text-gray-500">{t('dyn.result.confidence')}{field.needsHumanReview ? t('dyn.result.needs_review') : ''}</span>
          <div className="mt-0.5 text-sm font-semibold text-gray-800">
            {Math.round(field.confidence * 100)}%{field.needsHumanReview ? ' ⚠️' : ''}
          </div>
        </div>
      </div>

      {/* 推理说明 / Reasoning */}
      {field.reasoning && (
        <div className="rounded-lg border border-purple-100 bg-purple-50 px-3 py-2 text-xs text-purple-800">
          <span className="font-semibold">{t('dyn.result.reasoning')}</span>
          {field.reasoning}
        </div>
      )}

      {/* 命中标签卡片 / Hit tag cards */}
      {field.tags && field.tags.length > 0 && (
        <div className="space-y-2">
          <span className="text-xs font-semibold text-gray-600">{t('dyn.result.hit_tags', field.tags.length)}</span>
          {field.tags.map((tag, i) => (
            <TagCard key={`${tag.ruleId}-${i}`} tag={tag} levels={levels} />
          ))}
        </div>
      )}

      {/* 被抑制标签（如有）/ Suppressed tags (if any) */}
      {field.suppressedTags && field.suppressedTags.length > 0 && (
        <div className="space-y-2 opacity-70">
          <span className="text-xs font-semibold text-gray-500">{t('dyn.result.suppressed_tags', field.suppressedTags.length)}</span>
          {field.suppressedTags.map((tag, i) => (
            <TagCard key={`sup-${tag.ruleId}-${i}`} tag={tag} levels={levels} />
          ))}
        </div>
      )}

      {/* 审计信息 + 原始 JSON / Audit info + raw JSON */}
      <AuditBar resp={resp} />
      <RawJson data={resp} />
    </div>
  );
}

/**
 * 记录级分类结果结构化视图。
 * Structured view for record-level classification results.
 *
 * 组成：记录级最终等级徽章 + 逐字段分类表格（字段名/最终等级/置信度/命中规则）+
 * 审计信息条 + 可折叠原始 JSON。
 * Composition: record-level final badge + per-field classification table
 * (field/final level/confidence/hit rules) + audit bar + collapsible raw JSON.
 */
function RecordResultView({ resp, levels }: { resp: ClassificationResponse; levels: StandardLevel[] }) {
  const { t } = useI18n(); // 读取翻译函数 / read translation function
  const record = resp.recordResult;
  if (!record) {
    return <RawJson data={resp} />;
  }
  // 逐字段结果条目 / per-field result entries
  const entries: [string, FieldClassificationResult][] = Object.entries(record.fieldResults ?? {});
  return (
    <div className="space-y-4">
      {/* 记录级最终等级 / Record-level final level */}
      <div className="flex items-center justify-between rounded-lg bg-white p-4 shadow-sm">
        <div className="flex items-center gap-3">
          <span
            className={`flex h-14 w-14 items-center justify-center rounded-xl text-2xl font-black ${levelSolidClass(record.finalLevel, levels)}`}
          >
            {record.finalLevel}
          </span>
          <div>
            <span className="text-xs text-gray-500">{t('dyn.result.record_final_level')}</span>
            <div className="mt-0.5 text-xs text-gray-500">{t('dyn.result.field_count', entries.length)}</div>
          </div>
        </div>
        <div className="text-right">
          <span className="text-xs text-gray-500">{t('dyn.result.confidence')}{record.needsHumanReview ? t('dyn.result.needs_review') : ''}</span>
          <div className="mt-0.5 text-sm font-semibold text-gray-800">
            {Math.round(record.confidence * 100)}%{record.needsHumanReview ? ' ⚠️' : ''}
          </div>
        </div>
      </div>

      {/* 逐字段分类表格 / Per-field classification table */}
      {entries.length > 0 && (
        <div className="overflow-hidden rounded-lg border border-gray-200">
          <table className="w-full text-left text-xs">
            <thead className="bg-gray-50 text-gray-500">
              <tr>
                <th className="px-3 py-2 font-medium">{t('dyn.result.table.field')}</th>
                <th className="px-3 py-2 font-medium">{t('dyn.result.table.level')}</th>
                <th className="px-3 py-2 font-medium">{t('dyn.result.table.confidence')}</th>
                <th className="px-3 py-2 font-medium">{t('dyn.result.table.rules')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {entries.map(([name, fr]) => (
                <tr key={name} className="bg-white">
                  <td className="px-3 py-2 font-mono text-gray-800">{name}</td>
                  <td className="px-3 py-2">
                    <LevelBadge levelId={fr.finalLevel} levels={levels} />
                  </td>
                  <td className="px-3 py-2 text-gray-600">{Math.round(fr.confidence * 100)}%</td>
                  <td className="px-3 py-2 text-gray-500">
                    {fr.tags?.map((t) => t.ruleId).filter(Boolean).join(', ') || '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* 审计信息 + 原始 JSON / Audit info + raw JSON */}
      <AuditBar resp={resp} />
      <RawJson data={resp} />
    </div>
  );
}

/**
 * 动态分类分级主组件 / Dynamic Classification Main Component
 *
 * 通过 tab 状态切换五个功能面板，每个面板独立维护输入/输出/加载状态。
 * Switches between five functional panels via tab state, each panel independently maintains input/output/loading state.
 */
export default function DynClassificationPanel() {
  /** 国际化翻译函数 / i18n translation function */
  const { t } = useI18n();
  /** 当前活动 Tab / Currently active tab */
  const [tab, setTab] = useState<'eval' | 'record' | 'generate' | 'info' | 'validate'>('eval');

  /* ====== 全局标准切换器状态 / Global Standard Switcher State ====== */
  /** 后端返回的标准详情列表（含等级体系） / Standard details list from backend (incl. level systems) */
  const [standards, setStandards] = useState<StandardDetail[]>([]);
  /** 标准列表加载中 / Standards list loading */
  const [standardsLoading, setStandardsLoading] = useState(true);
  /** 当前选中的标准 ID（空串 = 默认通用规则引擎） / Currently selected standard ID (empty = default engine) */
  const [currentStandard, setCurrentStandard] = useState('');

  /**
   * 面板挂载时拉取后端可用标准列表 / Fetch available standards from backend on mount
   *
   * 标准是分类分级的核心上下文：切换后所有评估请求（字段级/记录级）
   * 均携带新标准 ID，agent 侧随之加载对应的 taxonomy 与规则包，
   * 实现前端 → 控制台后端 → agent 全链路切换。
   * Standards are the core context of classification: after switching, all eval
   * requests carry the new standard ID and the agent loads the corresponding
   * taxonomy & rule packs, achieving full-chain switching.
   */
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const resp = await fetchStandards();
        if (!cancelled) setStandards(resp.details ?? []);
      } catch {
        /* 拉取失败不阻断面板使用，仅标准切换器为空 / Fetch failure doesn't block the panel */
      } finally {
        if (!cancelled) setStandardsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  /** 当前选中标准的详情（未选中时为 null） / Details of the selected standard (null when none) */
  const currentDetail: StandardDetail | null =
    standards.find((s) => s.standard_id === currentStandard) ?? null;

  /**
   * 切换标准 / Switch standard
   *
   * 切换后清空已有评估结果，避免展示旧标准下的结论造成误导。
   * Clears existing eval results after switching to avoid showing stale conclusions.
   */
  const handleStandardChange = (id: string) => {
    setCurrentStandard(id);
    evalAction.reset();   // 清空字段评估结果 / clear field eval result
    recordAction.reset(); // 清空记录分类结果 / clear record classification result
  };

  /* ====== 字段动态评估 (Eval) 状态 / Field Dynamic Evaluation State ====== */
  const [fieldName, setFieldName] = useState('mobile_phone');   // 字段名 / Field name
  const [fieldValue, setFieldValue] = useState('13800138000');  // 字段值 / Field value
  const [domain, setDomain] = useState('');                     // 领域（可选，标准优先）/ Domain (optional, standard takes precedence)
  /** 字段评估异步动作（data/loading/error 三态）/ Field eval async action (data/loading/error tri-state) */
  const evalAction = useAsyncAction<ClassificationResponse>();

  /* ====== 标准文档生成配置 (Generate) 状态 / Standard Doc Generate Config State ====== */
  const [docPath, setDocPath] = useState('docs/standard/四川省健康医疗大数据应用指南.md'); // 文档路径 / Doc path
  /** 配置生成异步动作 / Config generation async action */
  const genAction = useAsyncAction<GenerateProfileResponse>();

  /* ====== 系统信息查询 (Info) 状态 / System Info Query State ====== */
  /** 系统信息查询异步动作 / System info query async action */
  const infoAction = useAsyncAction<unknown>();

  /* ====== 规则校验 (Validate) 状态 / Rule Validation State ====== */
  /** 规则校验异步动作 / Rule validation async action */
  const valAction = useAsyncAction<ValidateResponse>();

  /* ====== 记录级分类 (Record) 状态 / Record-level Classification State ====== */
  const [recordJson, setRecordJson] = useState('{"name": "张三", "id_card": "110101199001011237", "phone": "13800138000"}'); // JSON 记录 / JSON record
  const [recordDomain, setRecordDomain] = useState('');                    // 领域（可选）/ Domain (optional)
  /** 记录分类异步动作 / Record classification async action */
  const recordAction = useAsyncAction<ClassificationResponse>();

  /**
   * 执行字段动态评估 / Execute Field Dynamic Evaluation
   *
   * 组装 payload（fieldName/value/domain/standard），
   * POST 到 /v1/dynclassification/eval 获取分类结果。
   * Assembles payload (fieldName/value/domain/standard),
   * POSTs to /v1/dynclassification/eval to get classification result.
   */
  const handleEval = () =>
    evalAction.run(async () => {
      const payload: Record<string, unknown> = { fieldName };
      if (fieldValue) payload.value = fieldValue;
      if (domain) payload.domain = domain;
      if (currentStandard) payload.standard = currentStandard;

      const res = await proxyRequest({
        method: 'POST',
        path: '/v1/dynclassification/eval',
        body: payload,
      });
      return res.data as ClassificationResponse;
    }, t('dyn.eval.error_fallback'));

  /**
   * 执行记录级分类 / Execute Record-level Classification
   *
   * 解析 JSON 记录，POST 到 /v1/dynclassification/eval_record，
   * 对记录中每个字段做分类分级。
   * Parses JSON record, POSTs to /v1/dynclassification/eval_record,
   * classifies each field in the record.
   */
  const handleRecordEval = () =>
    recordAction.run(async () => {
      let record: Record<string, unknown>;
      try {
        record = JSON.parse(recordJson);
      } catch {
        // JSON 解析失败时抛出带 i18n 文案的 Error，由 hook 统一捕获为 error 状态
        // Throw an i18n Error on parse failure; the hook captures it as error state
        throw new Error(t('dyn.record.json_error'));
      }
      const payload: Record<string, unknown> = { record };
      if (recordDomain) payload.domain = recordDomain;
      if (currentStandard) payload.standard = currentStandard;

      const res = await proxyRequest({
        method: 'POST',
        path: '/v1/dynclassification/eval_record',
        body: payload,
      });
      return res.data as ClassificationResponse;
    }, t('dyn.record.error_fallback'));

  /**
   * 执行标准文档自动生成配置 / Execute Standard Doc Auto-generate Config
   *
   * POST 到 /v1/dynclassification/generate_profile，
   * 从规范文档中自动提取分类规则并生成 YAML 配置。
   * POSTs to /v1/dynclassification/generate_profile,
   * auto-extracts classification rules from spec doc and generates YAML config.
   */
  const handleGenerate = () =>
    genAction.run(async () => {
      const res = await proxyRequest({
        method: 'POST',
        path: '/v1/dynclassification/generate_profile',
        body: { docPath },
      });
      return res.data as GenerateProfileResponse;
    }, t('dyn.gen.error_fallback'));

  /**
   * 查询系统信息（标准/领域/算子）/ Query System Info (Standards/Domains/Operators)
   *
   * GET 到 /v1/dynclassification/{type}，获取已注册的目录列表。
   * GETs /v1/dynclassification/{type}, retrieves registered directory list.
   *
   * @param type - 查询类型 / Query type
   */
  const handleFetchInfo = (type: 'standards' | 'domains' | 'operators') =>
    infoAction.run(async () => {
      const res = await proxyRequest({
        method: 'GET',
        path: `/v1/dynclassification/${type}`,
      });
      return res.data;
    }, t('dyn.info.error_fallback'));

  /**
   * 执行规则校验 / Execute Rule Validation
   *
   * POST 到 /v1/dynclassification/validate，
   * 校验当前 YAML 配置的完整性与一致性。
   * POSTs to /v1/dynclassification/validate,
   * validates completeness and consistency of current YAML config.
   */
  const handleValidate = () =>
    valAction.run(async () => {
      const res = await proxyRequest({
        method: 'POST',
        path: '/v1/dynclassification/validate',
      });
      return res.data as ValidateResponse;
    }, t('dyn.validate.error_fallback'));

  return (
    <div className="flex h-full flex-col bg-white">
      {/* 头部面板标题 */}
      <div className="border-b border-gray-100 bg-gradient-to-r from-purple-50 to-indigo-50 p-6">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-purple-600 text-white shadow-md shadow-purple-200">
            <Icon name="sparkles" className="h-6 w-6" />
          </div>
          <div>
            <h1 className="text-xl font-bold text-gray-900">{t('dyn.title')}</h1>
            <p className="text-xs text-gray-500">
              {t('dyn.subtitle')}
            </p>
          </div>
        </div>

        {/* 全局标准切换器：切换后所有的评估请求携带新标准，agent 侧加载对应 taxonomy 与规则包 */}
        {/* Global standard switcher: after switching, all eval requests carry the new standard */}
        <div className="mt-4 flex flex-wrap items-center gap-3 rounded-xl border border-purple-100 bg-white/70 px-4 py-3">
          <span className="text-xs font-semibold text-gray-700">{t('dyn.standard.label')}</span>
          <select
            value={currentStandard}
            onChange={(e) => handleStandardChange(e.target.value)}
            disabled={standardsLoading}
            aria-label={t('dyn.standard.label')}
            className="rounded-lg border border-purple-200 bg-white px-3 py-1.5 text-sm text-gray-800 focus:border-purple-500 focus:outline-none disabled:opacity-50"
          >
            <option value="">{standardsLoading ? t('dyn.standard.loading') : t('dyn.standard.default')}</option>
            {standards.map((s) => (
              <option key={s.standard_id} value={s.standard_id}>
                {s.standard_id} — {s.description}
              </option>
            ))}
          </select>
          {currentDetail ? (
            <div className="flex flex-wrap items-center gap-2">
              <span className="rounded-full bg-purple-100 px-2.5 py-0.5 text-xs font-semibold text-purple-700">
                {currentDetail.description}
              </span>
              {currentDetail.levels.map((lv) => (
                <span key={lv.id} title={lv.name}>
                  <LevelBadge levelId={lv.id} levels={currentDetail.levels} />
                </span>
              ))}
              <span className="text-xs text-gray-400">{t('dyn.standard.default_level', currentDetail.default_level)}</span>
              {typeof currentDetail.category_count === 'number' && (
                <span className="text-xs text-gray-400">{t('dyn.standard.category_count', currentDetail.category_count)}</span>
              )}
              {typeof currentDetail.rule_count === 'number' && (
                <span className="text-xs text-gray-400">{t('dyn.standard.rule_count', currentDetail.rule_count)}</span>
              )}
            </div>
          ) : (
            !standardsLoading && (
              <span className="text-xs text-gray-400">{t('dyn.standard.generic_hint')}</span>
            )
          )}
        </div>

        {/* Tab 导航切换（WAI-ARIA tablist 语义）/ Tab navigation (WAI-ARIA tablist semantics) */}
        <div className="mt-6 flex border-b border-gray-200" role="tablist" aria-label={t('dyn.title')}>
          <button
            role="tab"
            aria-selected={tab === 'eval'}
            onClick={() => setTab('eval')}
            className={`border-b-2 px-4 py-2 text-sm font-medium transition-colors ${
              tab === 'eval' ? 'border-purple-600 text-purple-600' : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            {t('dyn.tab.eval')}
          </button>
          <button
            role="tab"
            aria-selected={tab === 'record'}
            onClick={() => setTab('record')}
            className={`border-b-2 px-4 py-2 text-sm font-medium transition-colors ${
              tab === 'record' ? 'border-purple-600 text-purple-600' : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            {t('dyn.tab.record')}
          </button>
          <button
            role="tab"
            aria-selected={tab === 'generate'}
            onClick={() => setTab('generate')}
            className={`border-b-2 px-4 py-2 text-sm font-medium transition-colors ${
              tab === 'generate' ? 'border-purple-600 text-purple-600' : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            {t('dyn.tab.generate')}
          </button>
          <button
            role="tab"
            aria-selected={tab === 'info'}
            onClick={() => {
              setTab('info');
              handleFetchInfo('standards');
            }}
            className={`border-b-2 px-4 py-2 text-sm font-medium transition-colors ${
              tab === 'info' ? 'border-purple-600 text-purple-600' : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            {t('dyn.tab.info')}
          </button>
          <button
            role="tab"
            aria-selected={tab === 'validate'}
            onClick={() => {
              setTab('validate');
              handleValidate();
            }}
            className={`border-b-2 px-4 py-2 text-sm font-medium transition-colors ${
              tab === 'validate' ? 'border-purple-600 text-purple-600' : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            {t('dyn.tab.validate')}
          </button>
        </div>
      </div>

      {/* 主体卡片区域 */}
      <div className="flex-1 overflow-y-auto p-6">
        {/* TAB 1: 字段评估 */}
        {tab === 'eval' && (
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
              <h2 className="mb-4 text-base font-semibold text-gray-800">{t('dyn.eval.params_title')}</h2>
              <div className="space-y-4">
                <div>
                  <label className="block text-xs font-medium text-gray-700">{t('dyn.eval.field_name')}</label>
                  <input
                    type="text"
                    value={fieldName}
                    onChange={(e) => setFieldName(e.target.value)}
                    className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-purple-500 focus:outline-none"
                    placeholder="e.g. mobile_phone, patient_brca1_gene"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-700">{t('dyn.eval.field_value')}</label>
                  <input
                    type="text"
                    value={fieldValue}
                    onChange={(e) => setFieldValue(e.target.value)}
                    className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-purple-500 focus:outline-none"
                    placeholder="e.g. 13800138000, 110101199003072375"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-700">{t('dyn.eval.domain')}</label>
                  <input
                    type="text"
                    list="eval-domain-list"
                    value={domain}
                    onChange={(e) => setDomain(e.target.value)}
                    className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-purple-500 focus:outline-none"
                    placeholder={t('dyn.eval.domain_ph')}
                  />
                  {/* 领域包候选：优先取当前标准的 domains，未选标准时为空（仍可手输）/ Domain candidates from current standard */}
                  <datalist id="eval-domain-list">
                    {(currentDetail?.domains ?? []).map((d) => (
                      <option key={d} value={d} />
                    ))}
                  </datalist>
                  <p className="mt-1 text-xs text-gray-400">
                    {t('dyn.eval.standard_hint', currentDetail ? `${currentDetail.standard_id}（${currentDetail.description}）` : t('dyn.eval.standard_default'))}
                  </p>
                  {/* 快捷填入预设样本 */}
                  <div className="mt-3 flex flex-wrap items-center gap-2">
                    <span className="text-xs text-gray-500 font-medium">快速预设:</span>
                    <button
                      type="button"
                      onClick={() => {
                        setFieldName("id_card");
                        setFieldValue("445321193704139886");
                        setDomain("medical");
                      }}
                      className="rounded bg-purple-50 px-2 py-1 text-xs text-purple-700 hover:bg-purple-100 border border-purple-200"
                    >
                      💳 身份证号 (PII)
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        setFieldName("medical_history");
                        setFieldValue("门诊复诊记录：周某，男，42岁，梅毒螺旋体抗体阳性，HIV筛查阴性。");
                        setDomain("medical");
                      }}
                      className="rounded bg-purple-50 px-2 py-1 text-xs text-purple-700 hover:bg-purple-100 border border-purple-200"
                    >
                      📋 门诊极敏感主诉 (HIV/梅毒)
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        setFieldName("genomic_sequence");
                        setFieldValue("基因检测报告：BRCA1基因检测到 c.5266dupC 致病突变。");
                        setDomain("medical");
                      }}
                      className="rounded bg-purple-50 px-2 py-1 text-xs text-purple-700 hover:bg-purple-100 border border-purple-200"
                    >
                      🧬 人类基因与罕见病 (L5)
                    </button>
                  </div>
                </div>
                <button
                  onClick={handleEval}
                  disabled={evalAction.loading}
                  className="w-full rounded-lg bg-purple-600 py-2.5 text-sm font-semibold text-white shadow-md shadow-purple-100 transition-colors hover:bg-purple-700 disabled:opacity-50"
                >
                  {evalAction.loading ? t('dyn.eval.submitting') : t('dyn.eval.submit')}
                </button>
              </div>
            </div>

            {/* 评估结果显示 */}
            <div className="rounded-xl border border-gray-200 bg-gray-50 p-5 shadow-sm">
              <h2 className="mb-4 text-base font-semibold text-gray-800">{t('dyn.eval.result_title')}</h2>
              {evalAction.error && <div className="rounded-lg bg-red-50 p-3 text-xs text-red-600">{evalAction.error}</div>}
              {evalAction.data ? (
                <EvalResultView resp={evalAction.data} levels={currentDetail?.levels ?? []} />
              ) : (
                <div className="flex h-48 items-center justify-center text-xs text-gray-400">{t('dyn.eval.empty')}</div>
              )}
            </div>
          </div>
        )}

        {/* TAB 2: 记录级分类 */}
        {tab === 'record' && (
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
              <h2 className="mb-4 text-base font-semibold text-gray-800">{t('dyn.record.input_title')}</h2>
              <div className="space-y-4">
                <div>
                  <label className="block text-xs font-medium text-gray-700">{t('dyn.record.json_label')}</label>
                  <textarea
                    value={recordJson}
                    onChange={(e) => setRecordJson(e.target.value)}
                    rows={5}
                    className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 font-mono text-xs focus:border-purple-500 focus:outline-none"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-700">{t('dyn.record.domain')}</label>
                  <input
                    type="text"
                    list="record-domain-list"
                    value={recordDomain}
                    onChange={(e) => setRecordDomain(e.target.value)}
                    className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-purple-500 focus:outline-none"
                    placeholder={t('dyn.eval.domain_ph')}
                  />
                  {/* 领域包候选：同字段评估，取当前标准的 domains / Domain candidates same as field eval */}
                  <datalist id="record-domain-list">
                    {(currentDetail?.domains ?? []).map((d) => (
                      <option key={d} value={d} />
                    ))}
                  </datalist>
                  <p className="mt-1 text-xs text-gray-400">
                    {t('dyn.eval.standard_hint', currentDetail ? `${currentDetail.standard_id}（${currentDetail.description}）` : t('dyn.eval.standard_default'))}
                  </p>
                </div>
                <button
                  onClick={handleRecordEval}
                  disabled={recordAction.loading}
                  className="w-full rounded-lg bg-purple-600 py-2.5 text-sm font-semibold text-white shadow-md shadow-purple-100 transition-colors hover:bg-purple-700 disabled:opacity-50"
                >
                  {recordAction.loading ? t('dyn.record.submitting') : t('dyn.record.submit')}
                </button>
              </div>
            </div>

            <div className="rounded-xl border border-gray-200 bg-gray-50 p-5 shadow-sm">
              <h2 className="mb-4 text-base font-semibold text-gray-800">{t('dyn.record.result_title')}</h2>
              {recordAction.error && <div className="rounded-lg bg-red-50 p-3 text-xs text-red-600">{recordAction.error}</div>}
              {recordAction.data ? (
                <RecordResultView resp={recordAction.data} levels={currentDetail?.levels ?? []} />
              ) : (
                <div className="flex h-48 items-center justify-center text-xs text-gray-400">{t('dyn.record.empty')}</div>
              )}
            </div>
          </div>
        )}

        {/* TAB 3: 自动生成配置 */}
        {tab === 'generate' && (
          <div className="max-w-3xl space-y-6">
            <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
              <h2 className="text-base font-semibold text-gray-800">{t('dyn.gen.title')}</h2>
              <p className="mt-1 text-xs text-gray-500">
                {t('dyn.gen.desc')}
              </p>
              <div className="mt-4 space-y-4">
                <div>
                  <label className="block text-xs font-medium text-gray-700">{t('dyn.gen.path_label')}</label>
                  <input
                    type="text"
                    value={docPath}
                    onChange={(e) => setDocPath(e.target.value)}
                    className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-purple-500 focus:outline-none"
                  />
                </div>
                <button
                  onClick={handleGenerate}
                  disabled={genAction.loading}
                  className="rounded-lg bg-purple-600 px-5 py-2 text-sm font-semibold text-white shadow-md transition-colors hover:bg-purple-700 disabled:opacity-50"
                >
                  {genAction.loading ? t('dyn.gen.submitting') : t('dyn.gen.submit')}
                </button>
              </div>
            </div>

            {genAction.error && <div className="rounded-lg bg-red-50 p-4 text-xs text-red-600">{genAction.error}</div>}
            {genAction.data && (
              <div className="rounded-xl border border-green-200 bg-green-50 p-5">
                <h3 className="text-sm font-bold text-green-800">{t('dyn.gen.success')}</h3>
                <p className="mt-1 text-xs text-green-700">{genAction.data.message}</p>
                <div className="mt-3 overflow-hidden rounded-lg bg-gray-900 p-3 text-xs text-green-400">
                  <pre>{JSON.stringify(genAction.data.generated_files, null, 2)}</pre>
                </div>
              </div>
            )}
          </div>
        )}

        {/* TAB 4: 目录查询 */}
        {tab === 'info' && (
          <div className="space-y-4">
            <div className="flex gap-2">
              <button
                onClick={() => handleFetchInfo('standards')}
                className="rounded-lg bg-purple-50 px-3 py-1.5 text-xs font-semibold text-purple-700 hover:bg-purple-100"
              >
                {t('dyn.info.standards')}
              </button>
              <button
                onClick={() => handleFetchInfo('domains')}
                className="rounded-lg bg-purple-50 px-3 py-1.5 text-xs font-semibold text-purple-700 hover:bg-purple-100"
              >
                {t('dyn.info.domains')}
              </button>
              <button
                onClick={() => handleFetchInfo('operators')}
                className="rounded-lg bg-purple-50 px-3 py-1.5 text-xs font-semibold text-purple-700 hover:bg-purple-100"
              >
                {t('dyn.info.operators')}
              </button>
            </div>
            {infoAction.error && <div className="rounded-lg bg-red-50 p-3 text-xs text-red-600">{infoAction.error}</div>}
            <div className="overflow-hidden rounded-xl border border-gray-200 bg-gray-900 p-4 text-xs text-green-400">
              {infoAction.loading ? <p>{t('dyn.info.loading')}</p> : <pre>{JSON.stringify(infoAction.data, null, 2)}</pre>}
            </div>
          </div>
        )}

        {/* TAB 5: 规则校验 */}
        {tab === 'validate' && (
          <div className="space-y-4">
            <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
              <div className="flex items-center justify-between">
                <div>
                  <h2 className="text-base font-semibold text-gray-800">{t('dyn.validate.title')}</h2>
                  <p className="text-xs text-gray-500">{t('dyn.validate.desc')}</p>
                </div>
                <button
                  onClick={handleValidate}
                  disabled={valAction.loading}
                  className="rounded-lg bg-purple-600 px-4 py-2 text-xs font-semibold text-white hover:bg-purple-700"
                >
                  {t('dyn.validate.resubmit')}
                </button>
              </div>
            </div>
            {valAction.error && <div className="rounded-lg bg-red-50 p-3 text-xs text-red-600">{valAction.error}</div>}
            {valAction.data && (
              <div className="overflow-hidden rounded-xl border border-gray-200 bg-gray-900 p-4 text-xs text-green-400">
                <pre>{JSON.stringify(valAction.data, null, 2)}</pre>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
