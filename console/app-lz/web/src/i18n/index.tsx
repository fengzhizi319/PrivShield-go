/**
 * 国际化 (i18n) 模块 — 提供中英文双语支持。
 *
 * 架构设计：
 *  1. 使用 React Context + Provider 模式，任何子组件可通过 useI18n() Hook 获取翻译函数
 *  2. 翻译表使用扁平的 key-value 结构，key 格式为 '模块.字段名'
 *  3. 默认语言为 zh-CN，当某个 key 在当前语言中缺失时回退到 zh-CN
 *
 * 使用方式：
 *  const { lang, setLang, t } = useI18n();
 *  <span>{t('topo.title')}</span>
 *
 * 翻译分组（按面板组件）：
 *  - app.*   : 应用标题与导航
 *  - topo.*  : 拓扑大屏
 *  - tasks.* : 任务生命周期与租约
 *  - runner.*: E2E 测试套件
 *  - audit.* : 审计验真
 *  - metrics.*: 性能指标
 *  - dataApi.*: 预设数据 API
 */
import React, { createContext, useContext, useState, ReactNode } from 'react';

/** 支持的语言类型 */
export type Language = 'zh-CN' | 'en-US';

/** i18n Context 提供的接口：当前语言、切换语言、翻译函数 */
export interface I18nContextType {
  /** 当前语言 */
  lang: Language;
  /** 切换语言 */
  setLang: (lang: Language) => void;
  /** 翻译函数：根据 key 返回当前语言的翻译文本 */
  t: (key: string) => string;
}

/**
 * 翻译表 — 扁平 key-value 结构，按面板分组。
 * key 格式：'模块.字段名'，如 'topo.title' / 'tasks.filter.all'
 */
const translations: Record<Language, Record<string, string>> = {
  'zh-CN': {
    // App Header & Nav
    'app.title': '数盾 · 调度之眼',
    'app.subtitle': '数据服务调度中枢 (Service Hub) 全景测试与治理工作台',
    'nav.topology': '四服务集群拓扑',
    'nav.benchmark': '性能与吞吐量压测',
    'nav.tasks': '任务生命周期与租约',
    'nav.runner': '自动化测试套件',
    'nav.audit': '不可篡改审计验真',
    'nav.metrics': '实时性能与分位数',
    'nav.dataApi': '预设数据 API 测试',

    // Benchmark & Stress Test
    'bench.title': '全栈微服务性能与吞吐量基准压测工作台',
    'bench.desc': '支持医保 (18字段) 与康养 (27字段) 全流程进行多协程并发压测，实时度量 QPS、TPS、5阶段耗时瀑布流与 P50/P90/P99 延迟 SLA。',
    'bench.presetTitle': '压测场景与预设套件',
    'bench.presetYibao': '医保结算全流程 (api1_yibao · 18字段)',
    'bench.presetYibaoDesc': '涵盖医保卡号、身份证号、医疗险别、自付金额等 18 字段，测试高敏剥离与自适应掩码全流程。',
    'bench.presetKangyang': '康养慢病全流程 (api2_kangyang · 27字段)',
    'bench.presetKangyangDesc': '涵盖心率、收缩压、血糖、病史体征等 27 字段，测试密集分类评级与隐私脱敏全流程。',
    'bench.presetBurst': '高并发突发脉冲测试 (Burst 50 并发)',
    'bench.presetBurstDesc': '短时间高并发脉冲打入，验证系统在瞬时峰值下的令牌桶限流与自适应排队保护。',
    'bench.presetCustom': '自定义并发与批次压测',
    'bench.presetCustomDesc': '自主配置并发协程数、请求总量与单次采样行数，执行定制化压力测试。',
    'bench.concurrency': '并发协程数 (Concurrency)',
    'bench.totalRequests': '总请求数 (Total Requests)',
    'bench.sampleLimit': '单次数据行数 (Batch Rows)',
    'bench.startBtn': '🚀 启动全链路压测',
    'bench.stopBtn': '⏹️ 终止压测',
    'bench.running': '压测执行中...',
    'bench.qps': '实时吞吐量 (QPS)',
    'bench.p50': '中位数延迟 (P50)',
    'bench.p90': 'P90 延迟',
    'bench.p95': 'P95 延迟 (SLA基准)',
    'bench.p99': 'P99 尾延迟 (Tail)',
    'bench.successRate': '全流程成功率',
    'bench.rateLimited': '令牌桶限流拦截 (429)',
    'bench.waterfallTitle': '5阶段端到端全流程单笔耗时瀑布流 (单请求平均)',
    'bench.stageIngest': '1. Ingest 入站接入校验与限流',
    'bench.stageFetch': '2. Fetch 数据源原始切片抽取',
    'bench.stageClassify': '3. Classify/Desensitize 规则评级与自适应脱敏',
    'bench.stageReturn': '4. Return 交付装配与响应封装',
    'bench.stageAudit': '5. Audit 不可篡改 SHA-256 存证落库',
    'bench.stageOverhead': '6. 网络传输 + JSON 序列化开销',
    'bench.slaCheck': 'SLA 质量评估与稳定性判定',
    'bench.slaP50': 'P50 延迟达标 (<100ms)',
    'bench.slaP99': 'P99 尾延迟达标 (<500ms)',
    'bench.slaSuccess': '高可用成功率 (>99.9%)',
    'bench.historyTitle': '压测历史与基准对比 (Historical Runs)',
    'bench.exportReport': '导出压测报告 (Markdown)',
    'bench.exportJson': '导出原始数据 (JSON)',
    'bench.copySuccess': '已复制到剪贴板！',
    'bench.liveLogTitle': '实时会话采样与存证流 (Live Stream)',

    // Topology
    'topo.title': '四微服务网格拓扑与健康矩阵',
    'topo.desc': '实时探测固定 4 微服务节点（1. 调度中枢 ➔ 2. 隐私与分类引擎 ➔ 3. 数据源管理 ➔ 4. 脱敏审计日志）连通性与延时。',
    'topo.refresh': '并发探测全集群',
    'topo.probing': '探测中...',
    'topo.allHealthy': '集群全部就绪',
    'topo.degraded': '集群部分降级',
    'topo.rtt': '往返延时 RTT',
    'topo.status': '状态',
    'topo.addr': '地址与端口',
    'topo.protocol.title': '通信协议通道选择',
    'topo.protocol.rest': 'REST (HTTP / JSON)',
    'topo.protocol.grpc': 'gRPC (mTLS / Protobuf)',
    'topo.protocol.restDesc': 'HTTP/1.1 REST JSON 接口，明文/标准 JSON 格式，供 Web 控制台、第三方前端与常规系统接入。',
    'topo.protocol.grpcDesc': 'HTTP/2 gRPC 二进制高性能接口，支持 mTLS 双向证书鉴权、SPKI 公钥固定与微服务高吞吐编排。',
    'topo.fixedOrder': '四服务固定全景顺序: 1. 调度中枢 ➔ 2. 隐私与分类引擎 ➔ 3. 数据源管理 ➔ 4. 脱敏审计日志',

    // Tasks & Leases
    'tasks.title': '任务全生命周期与 Phase B 租约看板',
    'tasks.desc': '按状态过滤检索历史任务，并对 PostgreSQL 多副本 FOR UPDATE SKIP LOCKED 原子租约进行深度观测。',
    'tasks.filter.all': '全部状态',
    'tasks.filter.pending': '等待中 (Pending)',
    'tasks.filter.running': '运行中 (Running)',
    'tasks.filter.completed': '已完成 (Completed)',
    'tasks.filter.failed': '失败 (Failed)',
    'tasks.id': '任务唯一 ID',
    'tasks.stage': '当前阶段',
    'tasks.duration': '耗时 (ms)',
    'tasks.created': '创建时间',
    'tasks.leaseOwner': '租约持有 Worker',
    'tasks.leaseExpiry': '租约剩余 (s)',
    'tasks.viewDetail': '查看详情',
    'tasks.leaseTitle': 'Phase B PostgreSQL 原子租约争抢视图',
    'tasks.leaseDesc': '展示多 Worker 节点在并发认领任务时的行锁状态与孤儿任务自愈回收。',

    // Test Suite Runner
    'runner.title': '自动化测试套件 (TS-01 / TS-02 / TS-03)',
    'runner.desc': '执行审计存证验真、高并发压测与 Phase B 租约争抢的测试用例。',
    'runner.runAll': '一键执行全部套件',
    'runner.runSelected': '执行选应用例',
    'runner.running': '测试执行中...',
    'runner.exportReport': '导出测试报告 (Markdown)',
    'runner.concurrency': '并发协程数',
    'runner.benchRequests': '压测总请求量',
    'runner.passRate': '测试通过率',
    'runner.assertions': '断言详情',
    'runner.terminalLogs': '实时执行日志流',

    // Audit Log & Merkle
    'audit.title': '不可篡改脱敏审计存证与 Merkle 验真',
    'audit.desc': '直连 audit-log 校验流水线产生的脱敏存证记录，在线触发 Merkle Tree 链式防篡改验真。',
    'audit.verifyBtn': '触发 Merkle 树完整性验真',
    'audit.verifying': '验真中...',
    'audit.merkleValid': 'Merkle 链完整有效 (未被篡改)',
    'audit.rootHash': 'Merkle Root Hash',
    'audit.totalEntries': '存证总笔数',
    'audit.signature': '防篡改数字签名',

    // Metrics
    'metrics.title': '实时性能指标与分位数监控',
    'metrics.desc': '监控 service-hub 的实时 QPS、6 阶段耗时瀑布图与 P50 / P90 / P95 / P99 延迟分位数。',
    'metrics.qps': '实时调度 QPS',
    'metrics.waterfall': '6 阶段平均耗时瀑布图 (ms)',
    'metrics.p50Desc': '50% 的请求耗时低于该值，代表中位数典型体验',
    'metrics.p90Desc': '90% 的请求耗时低于该值，反映大多数用户实际延迟',
    'metrics.p95Desc': '95% 的请求耗时低于该值，核心 SLA 达标基准线',
    'metrics.p99Desc': '99% 的请求耗时低于该值，排查极端长尾与垃圾回收停顿',

    // Data API Session
    'dataApi.title': '预设数据 API 全链路会话测试',
    'dataApi.desc': 'service-hub 与 datasource-mgr 之间预设 4 个业务数据 API。点击“申请”触发完整链路：前端 → service-hub 调度 → datasource-mgr 拉取原始数据 → engine 分类脱敏 → audit-log 存证 → 前端展示。',
    'dataApi.flowTitle': '全链路会话流转',
    'dataApi.sampleLimit': '采样条数',
    'dataApi.invokeBtn': '申请数据 (触发全链路)',
    'dataApi.invoking': '会话执行中...',
    'dataApi.reserved': '预留接口，待后续业务接入',
    'dataApi.sessionResult': '会话结果',
    'dataApi.dataDiff': '原始数据 vs 脱敏数据',
    'dataApi.showRaw': '展示原始 JSON',
    'dataApi.hideRaw': '隐藏原始 JSON',
    'dataApi.rawRecords': '原始明文记录',
    'dataApi.sanitizedRecords': '脱敏合规记录',
    'dataApi.auditWritten': '审计存证 ID',

    // Pipeline
    'pipe.title': '6 阶段流水线动态流转大屏',
    'pipe.desc': '全景透视数据调度处理 6 阶段（接收校验 → 数据拉取 → 分类评级 → 隐私脱敏 → 结果装配 → 审计存证）运行时状态与执行流水。',

    // Datasource
    'ds.title': '多源异构数据切片浏览器',
    'ds.desc': '在线探查与切片采样数据源，并支持联动调度中枢触发数据合规治理。',
    'ds.sampleSlice': '采样切片',
  },
  'en-US': {
    // App Header & Nav
    'app.title': 'PrivShield · Eye of Hub (App-LZ)',
    'app.subtitle': 'Service Hub E2E Testing, Full-Chain Observability & Mesh Governance Console',
    'nav.topology': 'Cluster Topology',
    'nav.benchmark': 'Performance & Benchmark',
    'nav.tasks': 'Task & Lease',
    'nav.runner': 'E2E Test Suites',
    'nav.audit': 'Audit & Merkle',
    'nav.metrics': 'Metrics & Percentiles',
    'nav.dataApi': 'Preset Data APIs',

    // Benchmark & Stress Test
    'bench.title': 'Full-Stack Performance & Throughput Benchmark Studio',
    'bench.desc': 'Execute multi-threaded stress tests on Medical (18 fields) & Healthcare (27 fields) pipelines with live QPS, TPS, 5-stage latency waterfall, and P50/P90/P99 percentiles.',
    'bench.presetTitle': 'Benchmark Scenarios & Presets',
    'bench.presetYibao': 'Medical Insurance Settlement (api1_yibao · 18 fields)',
    'bench.presetYibaoDesc': 'Full-flow test covering 18 fields (ID card, phone, insurance tier, copay) with sensitive masking & audit.',
    'bench.presetKangyang': 'Elderly Healthcare Vitals (api2_kangyang · 27 fields)',
    'bench.presetKangyangDesc': 'Full-flow test covering 27 fields (heart rate, blood pressure, glucose, medical history) with dynamic classification.',
    'bench.presetBurst': 'Burst Spike Stress Test (50 Concurrency)',
    'bench.presetBurstDesc': 'Inject sudden high concurrency spikes to verify Token Bucket rate limiting and overload protection.',
    'bench.presetCustom': 'Custom Concurrency & Batch Stress Test',
    'bench.presetCustomDesc': 'Configure custom concurrency, request volume, and batch size to perform tailored stress benchmarks.',
    'bench.concurrency': 'Concurrency Workers',
    'bench.totalRequests': 'Total Requests',
    'bench.sampleLimit': 'Batch Rows per Request',
    'bench.startBtn': '🚀 Start Benchmark',
    'bench.stopBtn': '⏹️ Stop Benchmark',
    'bench.running': 'Benchmarking in progress...',
    'bench.qps': 'Throughput (QPS / RPS)',
    'bench.p50': 'Median Latency (P50)',
    'bench.p90': 'P90 Latency',
    'bench.p95': 'P95 Latency (Core SLA)',
    'bench.p99': 'P99 Tail Latency',
    'bench.successRate': 'Full-Flow Success Rate',
    'bench.rateLimited': 'Token Bucket Protected (429)',
    'bench.waterfallTitle': '5-Stage Full-Flow Latency Waterfall Breakdown (Avg per Request)',
    'bench.stageIngest': '1. Ingest Inbound Validation & Rate Limiting',
    'bench.stageFetch': '2. Fetch Datasource Raw Slice Extraction',
    'bench.stageClassify': '3. Classify/Desensitize Rule Engine & Adaptive Masking',
    'bench.stageReturn': '4. Return Payload Packaging & Delivery',
    'bench.stageAudit': '5. Audit Immutable SHA-256 Proof Persistence',
    'bench.stageOverhead': '6. Network Transfer + JSON Serialization Overhead',
    'bench.slaCheck': 'SLA Quality & Reliability Assessment',
    'bench.slaP50': 'P50 Latency Compliant (<100ms)',
    'bench.slaP99': 'P99 Tail Latency Compliant (<500ms)',
    'bench.slaSuccess': 'High Availability (>99.9%)',
    'bench.historyTitle': 'Benchmark History & Baseline Comparison',
    'bench.exportReport': 'Export Report (Markdown)',
    'bench.exportJson': 'Export Raw Data (JSON)',
    'bench.copySuccess': 'Copied to clipboard!',
    'bench.liveLogTitle': 'Live Request Sampling & Audit Proof Stream',

    // Topology
    'topo.title': '4-Microservice Topology & Health Matrix',
    'topo.desc': 'Real-time probe of fixed 4-service mesh (1. Service Hub ➔ 2. PrivShield Agent ➔ 3. Datasource Mgr ➔ 4. Audit Log).',
    'topo.refresh': 'Probe All Services',
    'topo.probing': 'Probing...',
    'topo.allHealthy': 'All Services Ready',
    'topo.degraded': 'Cluster Degraded',
    'topo.rtt': 'Round-Trip RTT',
    'topo.status': 'Status',
    'topo.addr': 'Address & Port',
    'topo.protocol.title': 'Protocol Channel Selection',
    'topo.protocol.rest': 'REST (HTTP / JSON)',
    'topo.protocol.grpc': 'gRPC (mTLS / Protobuf)',
    'topo.protocol.restDesc': 'HTTP/1.1 REST JSON endpoints for Web Consoles, standard client integrations, and ease of inspection.',
    'topo.protocol.grpcDesc': 'HTTP/2 gRPC binary high-throughput endpoints with mTLS mutual certificate authentication and SPKI pinning.',
    'topo.fixedOrder': 'Fixed Service Order: 1. Service Hub ➔ 2. PrivShield Agent ➔ 3. Datasource Mgr ➔ 4. Audit Log',

    // Tasks & Leases
    'tasks.title': 'Task Lifecycle & Phase B Lease Inspector',
    'tasks.desc': 'Filter historical tasks by status and observe PostgreSQL multi-worker FOR UPDATE SKIP LOCKED atomic leases.',
    'tasks.filter.all': 'All Statuses',
    'tasks.filter.pending': 'Pending',
    'tasks.filter.running': 'Running',
    'tasks.filter.completed': 'Completed',
    'tasks.filter.failed': 'Failed',
    'tasks.id': 'Task ID',
    'tasks.stage': 'Stage',
    'tasks.duration': 'Duration (ms)',
    'tasks.created': 'Created At',
    'tasks.leaseOwner': 'Lease Worker',
    'tasks.leaseExpiry': 'Lease TTL (s)',
    'tasks.viewDetail': 'View Details',
    'tasks.leaseTitle': 'Phase B PostgreSQL Atomic Lease Inspector',
    'tasks.leaseDesc': 'Live display of worker task claims, row-level locks, and orphan lease reclamation.',

    // Test Suite Runner
    'runner.title': 'E2E Test Suites (TS-01 / TS-02 / TS-03)',
    'runner.desc': 'Execute audit verification, concurrency stress test, and atomic lease contention test cases.',
    'runner.runAll': 'Run All Test Suites',
    'runner.runSelected': 'Run Selected Suites',
    'runner.running': 'Executing Suites...',
    'runner.exportReport': 'Export Markdown Report',
    'runner.concurrency': 'Concurrency Workers',
    'runner.benchRequests': 'Total Benchmark Requests',
    'runner.passRate': 'Pass Rate',
    'runner.assertions': 'Assertions',
    'runner.terminalLogs': 'Terminal Execution Logs',

    // Audit Log & Merkle
    'audit.title': 'Immutable Audit Log & Merkle Verification',
    'audit.desc': 'Inspect SHA-256 audit entries and trigger on-demand Merkle Tree verification for tamper-proof compliance.',
    'audit.verifyBtn': 'Verify Merkle Integrity',
    'audit.verifying': 'Verifying...',
    'audit.merkleValid': 'Merkle Tree Valid (Tamper-Free)',
    'audit.rootHash': 'Merkle Root Hash',
    'audit.totalEntries': 'Total Entries',
    'audit.signature': 'Digital Signature',

    // Metrics
    'metrics.title': 'Live Performance Metrics & Latency Percentiles',
    'metrics.desc': 'Monitor real-time QPS, 6-stage duration breakdown waterfall, and P50 / P90 / P95 / P99 latency percentiles.',
    'metrics.qps': 'Real-time QPS',
    'metrics.waterfall': '6-Stage Duration Waterfall (ms)',
    'metrics.p50Desc': '50% of requests complete within this time (median experience)',
    'metrics.p90Desc': '90% of requests complete within this time (majority experience)',
    'metrics.p95Desc': '95% of requests complete within this time (core SLA baseline)',
    'metrics.p99Desc': '99% of requests complete within this time (tail latency & GC pauses)',

    // Data API Session
    'dataApi.title': 'Preset Data API Full-Chain Session Test',
    'dataApi.desc': '4 preset business data APIs between service-hub and datasource-mgr. Click "Apply" to trigger the full chain: Frontend → service-hub dispatch → datasource-mgr fetch → engine classify & mask → audit-log store → Frontend display.',
    'dataApi.flowTitle': 'Full-Chain Session Flow',
    'dataApi.sampleLimit': 'Sample Limit',
    'dataApi.invokeBtn': 'Apply Data (Full Chain)',
    'dataApi.invoking': 'Executing Session...',
    'dataApi.reserved': 'Reserved, pending business integration',
    'dataApi.sessionResult': 'Session Result',
    'dataApi.dataDiff': 'Raw Data vs Sanitized Data',
    'dataApi.showRaw': 'Show Raw JSON',
    'dataApi.hideRaw': 'Hide Raw JSON',
    'dataApi.rawRecords': 'Raw Plaintext Records',
    'dataApi.sanitizedRecords': 'Sanitized Compliant Records',
    'dataApi.auditWritten': 'Audit Entry ID',

    // Pipeline
    'pipe.title': '6-Stage Pipeline Dynamic Flow Stream',
    'pipe.desc': 'Full visualization of 6 runtime stages in data governance pipeline.',

    // Datasource
    'ds.title': 'Heterogeneous Datasource Slice Explorer',
    'ds.desc': 'Inspect and sample heterogeneous datasets.',
    'ds.sampleSlice': 'Sample Slice',
  },
};

/** React Context 实例（初始为 null，由 I18nProvider 填充） */
const I18nContext = createContext<I18nContextType | null>(null);

/**
 * I18nProvider — 国际化上下文提供者。
 * 挂载在 main.tsx 中 App 组件的外层，为所有子组件提供翻译能力。
 *
 * 翻译回退策略：当前语言翻译 → zh-CN 回退 → 原始 key
 */
export const I18nProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [lang, setLang] = useState<Language>('zh-CN');

  /**
   * 翻译函数 — 根据 key 查找当前语言的翻译文本。
   * 回退链：translations[当前语言][key] → translations['zh-CN'][key] → key 本身
   */
  const t = (key: string): string => {
    return translations[lang]?.[key] || translations['zh-CN']?.[key] || key;
  };

  return (
    <I18nContext.Provider value={{ lang, setLang, t }}>
      {children}
    </I18nContext.Provider>
  );
};

/**
 * useI18n Hook — 在组件中获取国际化接口。
 * 必须在 I18nProvider 内部使用，否则抛出错误。
 *
 * @returns { lang, setLang, t } 当前语言、切换函数、翻译函数
 */
export const useI18n = (): I18nContextType => {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    throw new Error('useI18n must be used within an I18nProvider');
  }
  return ctx;
};
