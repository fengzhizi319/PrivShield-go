/**
 * 轻量级国际化 (i18n) 上下文 / Lightweight Internationalization (i18n) Context
 *
 * 自建的中英文切换方案，无外部依赖（不用 react-i18next / react-intl），
 * 通过 React Context + useState 实现，支持占位符替换与 localStorage 持久化。
 * Self-built zh/en switching solution with no external dependencies (no react-i18next / react-intl),
 * implemented via React Context + useState, supports placeholder replacement and localStorage persistence.
 *
 * 使用方式 / Usage：
 *   const { t, lang, setLang } = useI18n();
 *   <span>{t('header.health_ok')}</span>
 *   <span>{t('batch.summary', 10, 8, 2)}</span>  // 占位符 {0},{1},{2}
 *
 * 架构设计 / Architecture：
 *   - I18nProvider 包裹应用根部，提供 lang/setLang/t 三个值；
 *   - useI18n() Hook 在任意组件中获取翻译函数；
 *   - 语言偏好保存在 localStorage('console-lang')，刷新后保持。
 *   - I18nProvider wraps app root, provides lang/setLang/t values;
 *   - useI18n() Hook retrieves translation function in any component;
 *   - Language preference persisted in localStorage('console-lang'), survives refresh.
 */

/** 引入 React Context 相关 API / Import React Context related APIs */
import { createContext, useContext, useState, useCallback, type ReactNode } from 'react';

/** 支持的语言类型 / Supported language type */
export type Lang = 'zh' | 'en';

/**
 * 中文字典 / Chinese Dictionary
 *
 * 键为翻译 key（按组件分组的 dot notation），值为中文文本。
 * 支持 {0}, {1}, ... 占位符，由 t() 函数在运行时替换。
 * Key is translation key (dot notation grouped by component), value is Chinese text.
 * Supports {0}, {1}, ... placeholders, replaced at runtime by t() function.
 */
const zh: Record<string, string> = {
  // Header
  'header.brand_title': '数盾 PrivShield 控制台',
  'header.brand_subtitle': '数联天下 · 隐私治理边车 (Sidecar)',
  'header.detecting': '检测中…',
  'header.agent_ok': '数盾服务 正常',
  'header.agent_down': '数盾服务 不可达',
  'header.back_home': '返回总览',

  // Sidebar
  'sidebar.search_placeholder': '搜索接口…',
  'sidebar.overview': '接口总览',
  'sidebar.batch_test': '批量测试',
  'sidebar.file_test': '文件处理',
  'sidebar.lb_test': '负载均衡',
  'sidebar.dyn_classify': '通用动态分类分级',
  'sidebar.ops': '运维诊断',
  'sidebar.no_match': '未找到匹配的接口',
  'sidebar.rest_only': 'REST 协议接口',

  // Overview
  'overview.title': '接口总览',
  'overview.subtitle': '共 {0} 个接口 · {1} 个功能模块，点击卡片开始测试',
  'overview.enter_test': '进入测试',
  'overview.more': '+{0} 个更多…',

  // EndpointView
  'endpoint.back': '返回总览',
  'endpoint.request_body': '请求体',
  'endpoint.format': '格式化',
  'endpoint.curl_copied': '已复制',
  'endpoint.history': '历史',
  'endpoint.reload_sample': '重载示例',
  'endpoint.sending': '发送中…',
  'endpoint.send': '发送请求',
  'endpoint.get_no_body': 'GET 请求无需请求体',
  'endpoint.json_parse_error': '请求体 JSON 解析错误：{0}',
  'endpoint.json_format_error': 'JSON 格式错误：{0}',
  'endpoint.content_type_hint': 'Content-Type: {0}（二进制载荷由后端处理）',
  'endpoint.format_title': '格式化 / 校验 JSON',
  'endpoint.curl_title': '复制 cURL 命令',
  'endpoint.history_title': '请求历史',
  'endpoint.reload_title': '恢复示例请求',
  'endpoint.send_shortcut': '快捷键 Cmd/Ctrl + Enter',

  // ResponsePanel
  'response.empty': '发送请求后在此查看响应',
  'response.failed': '请求失败',
  'response.copy': '复制',
  'response.copied': '已复制',
  'response.download': '下载',
  'response.via_title': '处理本请求的控制台后端',
  'response.protocol_title': '后端与 agent 的通信协议',

  // BatchTest
  'batch.title': '批量测试',
  'batch.subtitle': '一键顺序调用所选分类下的全部接口，快速回归验证。单个失败不会中断整个批次。',
  'batch.scope': '测试范围',
  'batch.all_categories': '全部分类（{0} 个接口）',
  'batch.running': '测试中…',
  'batch.start': '开始测试（{0}）',
  'batch.all_passed': '全部通过',
  'batch.n_failed': '{0} 个失败',
  'batch.summary': '共 {0} · 通过 {1} · 失败 {2}',
  'batch.col_status': '状态',
  'batch.col_endpoint': '接口',
  'batch.col_duration': '耗时',
  'batch.col_info': '信息',
  'batch.empty_hint': '选择范围后点击"开始测试"',
  'batch.goto_endpoint': '跳转到该端点',

  // HistoryPanel
  'history.title': '请求历史（{0}）',
  'history.clear': '清空',
  'history.close': '关闭',
  'history.empty': '暂无历史记录',
  'history.body_empty': '(空)',
  'history.fill_title': '点击回填该请求体',
  'history.delete_title': '删除该记录',

  // LbTest
  'lb.title': '负载均衡测试',
  'lb.subtitle': '配置多个后端地址，按策略分发探测请求并对比各节点表现。',
  'lb.backends': '后端节点',
  'lb.add_node': '添加节点',
  'lb.name_placeholder': '名称',
  'lb.num_requests': '探测请求数',
  'lb.strategy': '分发策略',
  'lb.strategy_round_robin': '轮询 (round_robin)',
  'lb.strategy_random': '随机 (random)',
  'lb.strategy_least_conn': '最少连接 (least_connections)',
  'lb.running': '测试中…',
  'lb.run': '运行测试',
  'lb.empty_hint': '运行测试后在此查看各节点分发结果',
  'lb.total_requests': '总请求',
  'lb.success': '成功',
  'lb.failed': '失败',
  'lb.total_duration': '总耗时',
  'lb.col_node': '节点',
  'lb.col_distribution': '命中分布',
  'lb.col_hits': '命中数',
  'lb.col_success_rate': '成功率',
  'lb.col_avg_latency': '平均延迟',
  'lb.col_min_max_latency': '最小/最大延迟',
  'lb.at_least_one': '请至少填写一个后端地址',
  'lb.delete_node': '删除节点',

  // ConcurrencyTest（并发压测）
  'sidebar.concurrency_test': '并发压测',
  'concurrency.title': '并发压测',
  'concurrency.subtitle': '以指定并发度向 Agent 发送请求，统计延迟分布与吞吐量。',
  'concurrency.presets': '快捷路径',
  'concurrency.target_path': '目标路径',
  'concurrency.request_body': '请求体（JSON）',
  'concurrency.concurrency': '并发数',
  'concurrency.concurrency_hint': '同时发出的最大请求数',
  'concurrency.total_requests': '总请求数',
  'concurrency.run': '运行压测',
  'concurrency.running': '压测中…',
  'concurrency.target_agent': '目标 Agent',
  'concurrency.empty_hint': '配置参数后点击“运行压测”查看结果',
  'concurrency.invalid_json': '请求体 JSON 格式错误，请检查',
  'concurrency.qps': '吞吐量',
  'concurrency.success_rate': '成功率',
  'concurrency.total_time': '总耗时',
  'concurrency.success_failed': '成功/失败',
  'concurrency.latency_distribution': '延迟分布',
  'concurrency.avg': '平均',
  'concurrency.min': '最小',
  'concurrency.max': '最大',
  'concurrency.metric': '指标',
  'concurrency.value_ms': '数值 (ms)',
  'concurrency.meaning': '统计含义与解释',
  'concurrency.desc.p50': '50% 的请求在此耗时内完成（中位数，反映常规并发下的典型延迟水平）',
  'concurrency.desc.p90': '90% 的请求在此耗时内完成（9成用户的体验上限）',
  'concurrency.desc.p95': '95% 的请求在此耗时内完成（企业级 SLA 服务可用性核心基准）',
  'concurrency.desc.p99': '99% 的请求在此耗时内完成（长尾延迟 Tail Latency，反映仅 1% 最慢请求的极端卡顿表现）',
  'concurrency.desc.avg': '所有请求的算术平均耗时（总耗时 ÷ 请求数，易受极少数超慢请求拉偏）',
  'concurrency.desc.min': '单次最快请求耗时（无排队、热缓存下的理论最优响应速度）',
  'concurrency.desc.max': '单次最慢请求耗时（测试周期内的最大单次延迟）',
  'concurrency.guide.title': '性能指标与分位数 (P50/P90/P95/P99) 名词指南',
  'concurrency.guide.what_is_p': '什么是分位数 (Percentile)？',
  'concurrency.guide.what_is_p_desc': '分位数（如 P50、P90、P95、P99）是将本次测试中所有请求的耗时按从小到大排序后，处于指定百分比位置的耗时数值。例如 P90 = 15ms，意味着 90% 的请求耗时都 ≤ 15ms，仅有 10% 的请求耗时超过 15ms。',
  'concurrency.guide.why_not_avg': '为什么压测必须看分位数，而不能只看“平均值 (Avg)”？',
  'concurrency.guide.why_not_avg_desc': '平均值（Avg）容易被极少数异常慢的请求（如 GC 停顿、TCP 重传、锁竞争）严重“拉偏”，无法反映真实绝大多数用户的体验。例如 100 次请求中有 99 次是 1ms，1 次卡顿 5000ms，算出来的平均值是 51ms——它既抹杀了 99% 请求极致快速的事实，又掩盖了存在 5000ms 严重卡顿的缺陷。通过对比 P50（代表大众体验）与 P99（代表长尾毛刺），能精准洞察系统在高并发下的稳定性。',
  'concurrency.guide.how_to_use': '如何根据分位数进行调优？',
  'concurrency.guide.how_to_use_desc': '• P50 居高不下：说明基础业务逻辑或算法开销大，需优化代码与计算链路；\n• P50 极低但 P99 极高（长尾严重）：说明存在锁竞争、线程池耗尽、垃圾回收 (GC) 或下游队列阻塞，需针对长尾进行并发流控与连接池调优；\n• P95 达标率：通常作为云原生微服务与对外 API 接口 SLA（服务等级协议）的黄金考核指标。',

  // OpsPanel（运维诊断）
  'ops.title': '运维诊断',
  'ops.subtitle': '链路排障 · 引擎降级状态 · 依赖与模型检测 · 硬件加速',
  'ops.refresh': '刷新诊断',
  'ops.refreshing': '诊断中…',
    'ops.refresh_hint': '重新探测各引擎（运行中补装依赖/模型后点此刷新结论）',
  'ops.loading': '正在采集诊断信息…',
  'ops.copy': '复制命令',
  'ops.updated_at': '诊断时间：',
  'ops.chain.title': '链路诊断（问题出在哪一层？）',
  'ops.chain.frontend': '前端',
  'ops.chain.backend': '控制台后端',
  'ops.chain.agent': 'Agent',
  'ops.chain.frontend_hint': '页面正常渲染',
  'ops.chain.unknown': '未知',
  'ops.chain.no_data': '暂无数据',
  'ops.chain.down': '不可达',
  'ops.engines.title': '分类引擎状态（NER / LLM 降级到哪了？）',
  'ops.engines.ner': 'NER 引擎（Layer-2）',
  'ops.engines.llm': 'LLM 引擎（Layer-3）',
  'ops.engines.active': '当前激活',
  'ops.engines.degraded': '已降级跳过',
  'ops.engines.unavailable': '不可用',
  'ops.engines.fallback': '备选（未到达）',
  'ops.engines.runtime': '运行时',
  'ops.engines.not_initialized': '尚未初始化（无分类请求）',
  'ops.engines.auto_probe': '自动探测',
  'ops.engines.probe_detail': '查看动态探测详情（实际尝试初始化各引擎）',
  'ops.engines.llm_ok': '可用',
  'ops.engines.backend': '后端',
  'ops.engines.model': '模型',
  'ops.deps.title': '依赖与驱动（是否安装 / 如何安装）',
  'ops.deps.col_name': '依赖',
  'ops.deps.col_status': '状态',
  'ops.deps.col_version': '版本',
  'ops.deps.col_purpose': '用途',
  'ops.deps.col_install': '安装命令',
  'ops.deps.installed': '已安装',
  'ops.deps.missing': '未安装',
  'ops.models.title': '模型文件',
  'ops.models.col_name': '模型',
  'ops.models.col_path': '路径',
  'ops.models.col_status': '状态',
  'ops.models.col_download': '下载命令',
  'ops.models.exists': '已就位',
  'ops.models.missing': '缺失',
  'ops.hardware.title': '硬件加速',
  'ops.hardware.cuda': 'CUDA',
  'ops.hardware.on': '可用',
  'ops.hardware.off': '不可用',
  'ops.hardware.unknown': '未知（torch 未加载）',
  'ops.hardware.nvidia_smi': 'nvidia-smi',
  'ops.hardware.found': '已找到',
  'ops.hardware.not_found': '未找到',
  'ops.hardware.platform': '运行平台',

  // DynClassificationPanel（动态分类分级）
  'dyn.title': '声明式通用动态分类分级 (Dynamic Classification)',
  'dyn.subtitle': '支持多领域、多行业标准（Sichuan/GD/Financial等）YAML 配置、开箱即用匹配算子与规范文档自动提取配置生成。',
  'dyn.standard.label': '当前标准 (Standard)',
  'dyn.standard.loading': '加载中…',
  'dyn.standard.default': '默认（通用规则引擎）',
  'dyn.standard.generic_hint': '使用通用规则引擎（未选择标准）',
  'dyn.standard.default_level': '默认等级: {0}',
  'dyn.standard.rule_count': '规则数: {0}',
  'dyn.standard.category_count': '分类数: {0}',
  'dyn.tab.eval': '字段动态评估 (Eval)',
  'dyn.tab.record': '记录级分类 (Record)',
  'dyn.tab.generate': '标准文档一键生成配置 (Auto Generate)',
  'dyn.tab.info': '标准/领域/算子目录 (Directory)',
  'dyn.tab.validate': '规则在线校验 (Validate)',
  // Eval tab
  'dyn.eval.params_title': '评估参数输入',
  'dyn.eval.field_name': '字段名称 (fieldName)',
  'dyn.eval.field_value': '字段数值 (value, 可选)',
  'dyn.eval.domain': '领域包 (domain, 可选)',
  'dyn.eval.domain_ph': 'general-pii / medical',
  'dyn.eval.standard_hint': '分类标准由顶部切换器控制：{0}',
  'dyn.eval.standard_default': '默认通用规则引擎',
  'dyn.eval.submit': '执行动态分类评估',
  'dyn.eval.submitting': '评估计算中…',
  'dyn.eval.result_title': '评估结果',
  'dyn.eval.empty': '点击左侧“执行动态分类评估”获取求值结果',
  'dyn.eval.error_fallback': '评估失败',
  // Record tab
  'dyn.record.input_title': '记录级分类输入',
  'dyn.record.json_label': '记录 JSON（字段名 → 值）',
  'dyn.record.domain': '领域 (domain, 可选)',
  'dyn.record.submit': '执行记录级分类',
  'dyn.record.submitting': '分类计算中…',
  'dyn.record.result_title': '记录级分类结果',
  'dyn.record.empty': '点击左侧“执行记录级分类”获取结果',
  'dyn.record.json_error': 'JSON 格式错误，请检查输入',
  'dyn.record.error_fallback': '记录级分类失败',
  // Generate tab
  'dyn.gen.title': '标准 Markdown 规范文档一键提取 YAML 配置',
  'dyn.gen.desc': '支持输入符合地方或行业标准的规范文档（如《四川省健康医疗大数据应用指南.md》），自动识别分级矩阵并提取 YAML 配置文件。',
  'dyn.gen.path_label': '文档文件路径 (docPath)',
  'dyn.gen.submit': '一键自动生成全套 YAML 配置',
  'dyn.gen.submitting': '解析抽取中…',
  'dyn.gen.success': '生成成功！',
  'dyn.gen.error_fallback': '生成失败',
  // Info tab
  'dyn.info.standards': '可用标准 (Standards)',
  'dyn.info.domains': '领域匹配包 (Domains)',
  'dyn.info.operators': '注册算子库 (Operators)',
  'dyn.info.loading': '加载中…',
  'dyn.info.error_fallback': '查询系统信息失败',
  // Validate tab
  'dyn.validate.title': '规则 YAML 文件合法性在线校验',
  'dyn.validate.desc': '检测算子未找到错误、语法错误与拼写模糊纠错提示。',
  'dyn.validate.resubmit': '重新校验',
  'dyn.validate.error_fallback': '规则校验失败',
  // Result views
  'dyn.result.final_level': '最终判定敏感等级',
  'dyn.result.record_final_level': '记录级最终等级',
  'dyn.result.engine_layer': '引擎层: {0}',
  'dyn.result.field_count': '字段数: {0}',
  'dyn.result.confidence': '置信度',
  'dyn.result.needs_review': ' · 需复核',
  'dyn.result.reasoning': '推理说明：',
  'dyn.result.hit_tags': '命中标签 ({0})',
  'dyn.result.suppressed_tags': '被抑制标签 ({0})',
  'dyn.result.raw_json': '查看原始 JSON (Raw JSON)',
  'dyn.result.tag.rule': '规则:',
  'dyn.result.tag.engine': '引擎:',
  'dyn.result.tag.match': '匹配:',
  'dyn.result.tag.needs_review': '⚠️ 需人工复核',
  'dyn.result.tag.downgrade': '↓ 降级产生',
  'dyn.result.audit.duration': '耗时',
  'dyn.result.audit.rules_hit': '命中规则',
  'dyn.result.audit.standard': '标准:',
  'dyn.result.audit.domain': '领域:',
  'dyn.result.table.field': '字段',
  'dyn.result.table.level': '等级',
  'dyn.result.table.confidence': '置信度',
  'dyn.result.table.rules': '命中规则',

  // FileTest（数据文件隐私处理）
  'file.title': '数据文件隐私处理',
  'file.subtitle': '上传 CSV/JSON 文件，选择脱敏 / K-匿名操作。',
  'file.file_label': '数据文件',
  'file.selected': '已选择：{0}（{1} KB）',
  'file.sample_prefix': '示例文件：',
  'file.sample_fill_title': '填充 {0} 示例文件并直接用于处理',
  'file.sample_download_title': '下载 {0} 示例文件到本地',
  'file.op_label': '操作类型',
  'file.op_mask': '数据脱敏',
  'file.op_mask_hint': '对指定列做掩码脱敏',
  'file.op_kano': 'K-匿名',
  'file.op_kano_hint': '对准标识符列做 K-匿名泛化',
  'file.mask_cols': '脱敏列（逗号分隔）',
  'file.mask_context': '上下文（可选）',
  'file.mask_context_ph': '如：医疗场景',
  'file.qi_cols': '准标识符列 QI（逗号分隔）',
  'file.k_value': 'K 值',
  'file.submitting': '处理中…',
  'file.submit': '上传并处理',
  'file.no_file': '请先选择 CSV 或 JSON 文件',
  'file.err_ext': '仅支持 .csv 与 .json 文件',
  'file.err_size': '文件过大（{0} MB），上限 {1} MB',
  'file.original': '原始数据',
  'file.result': '处理结果',
  'file.rows_preview': '前 {0} 行 / 共 {1} 行',
  'file.changed': '已变更',
  'file.preview_unavailable': '预览不可用：{0}',
  'file.original_empty': '选择或填充示例文件后，在此预览原始数据',
  'file.result_no_records': '本次响应未返回记录数组，请查看上方原始响应 JSON',
  'file.result_empty': '处理完成后在此查看结果，变更单元格将高亮显示',

  // ErrorBoundary（错误边界）
  'error.title': '界面渲染出错',
  'error.unknown': '发生未知错误',
  'error.retry': '重试',

  // App
  'app.loading': '加载接口列表…',
  'app.connect_failed': '无法连接后端 {0}',
  'app.retry': '重试',
};

/**
 * 英文字典 / English Dictionary
 *
 * 与中文字典一一对应，键相同，值为英文文本。
 * Corresponds one-to-one with Chinese dictionary, same keys, English values.
 */
const en: Record<string, string> = {
  // Header
  'header.brand_title': 'PrivShield Console',
  'header.brand_subtitle': 'Shulian Tianxia · Privacy Governance Sidecar',
  'header.detecting': 'Checking…',
  'header.agent_ok': 'PrivShield OK',
  'header.agent_down': 'PrivShield Unreachable',
  'header.back_home': 'Back to Overview',

  // Sidebar
  'sidebar.search_placeholder': 'Search endpoints…',
  'sidebar.overview': 'Overview',
  'sidebar.batch_test': 'Batch Test',
  'sidebar.file_test': 'File Test',
  'sidebar.lb_test': 'Load Balancer',
  'sidebar.dyn_classify': 'Dynamic Classification',
  'sidebar.ops': 'Ops Diagnostics',
  'sidebar.no_match': 'No matching endpoints',
  'sidebar.rest_only': 'REST protocol endpoint',

  // Overview
  'overview.title': 'API Overview',
  'overview.subtitle': '{0} endpoints · {1} modules, click a card to start testing',
  'overview.enter_test': 'Enter Test',
  'overview.more': '+{0} more…',

  // EndpointView
  'endpoint.back': 'Back to Overview',
  'endpoint.request_body': 'Request Body',
  'endpoint.format': 'Format',
  'endpoint.curl_copied': 'Copied',
  'endpoint.history': 'History',
  'endpoint.reload_sample': 'Reload Sample',
  'endpoint.sending': 'Sending…',
  'endpoint.send': 'Send Request',
  'endpoint.get_no_body': 'GET requests have no body',
  'endpoint.json_parse_error': 'Request body JSON parse error: {0}',
  'endpoint.json_format_error': 'JSON format error: {0}',
  'endpoint.content_type_hint': 'Content-Type: {0} (binary payload handled by backend)',
  'endpoint.format_title': 'Format / validate JSON',
  'endpoint.curl_title': 'Copy cURL command',
  'endpoint.history_title': 'Request history',
  'endpoint.reload_title': 'Restore sample request',
  'endpoint.send_shortcut': 'Shortcut: Cmd/Ctrl + Enter',

  // ResponsePanel
  'response.empty': 'Send a request to view the response here',
  'response.failed': 'Request Failed',
  'response.copy': 'Copy',
  'response.copied': 'Copied',
  'response.download': 'Download',
  'response.via_title': 'Console backend that handled this request',
  'response.protocol_title': 'Backend-agent communication protocol',

  // BatchTest
  'batch.title': 'Batch Test',
  'batch.subtitle': 'Sequentially invoke all endpoints in the selected category for quick regression. A single failure won\'t abort the batch.',
  'batch.scope': 'Scope',
  'batch.all_categories': 'All Categories ({0} endpoints)',
  'batch.running': 'Testing…',
  'batch.start': 'Start Test ({0})',
  'batch.all_passed': 'All Passed',
  'batch.n_failed': '{0} Failed',
  'batch.summary': 'Total {0} · Passed {1} · Failed {2}',
  'batch.col_status': 'Status',
  'batch.col_endpoint': 'Endpoint',
  'batch.col_duration': 'Duration',
  'batch.col_info': 'Info',
  'batch.empty_hint': 'Select a scope and click "Start Test"',
  'batch.goto_endpoint': 'Go to this endpoint',

  // HistoryPanel
  'history.title': 'Request History ({0})',
  'history.clear': 'Clear',
  'history.close': 'Close',
  'history.empty': 'No history yet',
  'history.body_empty': '(empty)',
  'history.fill_title': 'Click to refill this request body',
  'history.delete_title': 'Delete this record',

  // LbTest
  'lb.title': 'Load Balancer Test',
  'lb.subtitle': 'Configure multiple backend addresses, distribute probe requests by strategy and compare node performance.',
  'lb.backends': 'Backend Nodes',
  'lb.add_node': 'Add Node',
  'lb.name_placeholder': 'Name',
  'lb.num_requests': 'Probe Requests',
  'lb.strategy': 'Strategy',
  'lb.strategy_round_robin': 'Round Robin',
  'lb.strategy_random': 'Random',
  'lb.strategy_least_conn': 'Least Connections',
  'lb.running': 'Testing…',
  'lb.run': 'Run Test',
  'lb.empty_hint': 'Run the test to view distribution results here',
  'lb.total_requests': 'Total',
  'lb.success': 'Success',
  'lb.failed': 'Failed',
  'lb.total_duration': 'Duration',
  'lb.col_node': 'Node',
  'lb.col_distribution': 'Distribution',
  'lb.col_hits': 'Hits',
  'lb.col_success_rate': 'Success Rate',
  'lb.col_avg_latency': 'Avg Latency',
  'lb.col_min_max_latency': 'Min/Max Latency',
  'lb.at_least_one': 'Please provide at least one backend address',
  'lb.delete_node': 'Delete node',

  // ConcurrencyTest (Concurrency Stress Test)
  'sidebar.concurrency_test': 'Concurrency Test',
  'concurrency.title': 'Concurrency Test',
  'concurrency.subtitle': 'Send requests to Agent at specified concurrency, collect latency distribution and throughput.',
  'concurrency.presets': 'Quick Paths',
  'concurrency.target_path': 'Target Path',
  'concurrency.request_body': 'Request Body (JSON)',
  'concurrency.concurrency': 'Concurrency',
  'concurrency.concurrency_hint': 'Maximum simultaneous requests',
  'concurrency.total_requests': 'Total Requests',
  'concurrency.run': 'Run Test',
  'concurrency.running': 'Testing…',
  'concurrency.target_agent': 'Target Agent',
  'concurrency.empty_hint': 'Configure parameters and click "Run Test" to view results',
  'concurrency.invalid_json': 'Request body JSON is invalid, please check',
  'concurrency.qps': 'Throughput',
  'concurrency.success_rate': 'Success Rate',
  'concurrency.total_time': 'Duration',
  'concurrency.success_failed': 'Success/Failed',
  'concurrency.latency_distribution': 'Latency Distribution',
  'concurrency.avg': 'Avg',
  'concurrency.min': 'Min',
  'concurrency.max': 'Max',
  'concurrency.metric': 'Metric',
  'concurrency.value_ms': 'Value (ms)',
  'concurrency.meaning': 'Statistical Meaning & Interpretation',
  'concurrency.desc.p50': '50% of requests completed within this time (Median, represents typical response latency under standard load)',
  'concurrency.desc.p90': '90% of requests completed within this time (Upper bound for 90% of user traffic)',
  'concurrency.desc.p95': '95% of requests completed within this time (Core benchmark for enterprise SLA service availability)',
  'concurrency.desc.p99': '99% of requests completed within this time (Tail Latency, captures worst-case delay experienced by the slowest 1% of requests)',
  'concurrency.desc.avg': 'Arithmetic mean latency (Total time ÷ Total requests, easily skewed by rare slow requests)',
  'concurrency.desc.min': 'Fastest single request duration (Theoretical optimal response without queueing or cache misses)',
  'concurrency.desc.max': 'Slowest single request duration (Maximum latency observed across the entire benchmark)',
  'concurrency.guide.title': 'Performance Metrics & Percentiles (P50/P90/P95/P99) Guide',
  'concurrency.guide.what_is_p': 'What is a Percentile (P50, P90, P95, P99)?',
  'concurrency.guide.what_is_p_desc': 'A percentile indicates the value below which a given percentage of observations falls when sorted in ascending order. For instance, P90 = 15ms means 90% of requests completed in 15ms or less, while only 10% exceeded 15ms.',
  'concurrency.guide.why_not_avg': 'Why use Percentiles instead of just the Average (Avg)?',
  'concurrency.guide.why_not_avg_desc': 'The average (arithmetic mean) is heavily skewed by rare outliers (e.g. GC pauses, lock contention, TCP retransmissions). For example, if 99 out of 100 requests take 1ms and 1 request takes 5000ms, the average is ~51ms. This average misrepresents the fast experience of 99% of users while hiding the 5000ms freeze. Comparing P50 (typical latency) against P99 (tail latency) provides an accurate, complete picture of system stability.',
  'concurrency.guide.how_to_use': 'How to optimize using Percentiles?',
  'concurrency.guide.how_to_use_desc': '• High P50: Core business logic or algorithm compute cost is high; optimize algorithms and pipelines.\n• Low P50 but High P99 (severe tail latency): Resource contention, thread pool starvation, or GC hiccups; tune concurrency limits, connection pools, and async queues.\n• P95 compliance: Commonly used as the primary SLA (Service Level Agreement) benchmark for cloud-native microservices.',

  // OpsPanel (Operations Diagnostics)
  'ops.title': 'Ops Diagnostics',
  'ops.subtitle': 'Chain troubleshooting · Engine degradation · Dependency & model checks · Hardware acceleration',
  'ops.refresh': 'Refresh',
  'ops.refreshing': 'Diagnosing…',
    'ops.refresh_hint': 'Re-probe engines (click after installing deps/models while the service is running)',
  'ops.loading': 'Collecting diagnostics…',
  'ops.copy': 'Copy command',
  'ops.updated_at': 'Diagnosed at:',
  'ops.chain.title': 'Chain Diagnosis (which layer fails?)',
  'ops.chain.frontend': 'Frontend',
  'ops.chain.backend': 'Console Backend',
  'ops.chain.agent': 'Agent',
  'ops.chain.frontend_hint': 'Page renders normally',
  'ops.chain.unknown': 'Unknown',
  'ops.chain.no_data': 'No data',
  'ops.chain.down': 'Unreachable',
  'ops.engines.title': 'Classification Engines (where did NER / LLM degrade to?)',
  'ops.engines.ner': 'NER Engine (Layer-2)',
  'ops.engines.llm': 'LLM Engine (Layer-3)',
  'ops.engines.active': 'Active',
  'ops.engines.degraded': 'Degraded past',
  'ops.engines.unavailable': 'Unavailable',
  'ops.engines.fallback': 'Fallback (unreached)',
  'ops.engines.runtime': 'Runtime',
  'ops.engines.not_initialized': 'Not initialized (no classify request yet)',
  'ops.engines.auto_probe': 'Auto Probe',
  'ops.engines.probe_detail': 'View dynamic probe details (actual engine initialization attempts)',
  'ops.engines.llm_ok': 'Available',
  'ops.engines.backend': 'Backend',
  'ops.engines.model': 'Model',
  'ops.deps.title': 'Dependencies & Drivers (installed? how to install?)',
  'ops.deps.col_name': 'Dependency',
  'ops.deps.col_status': 'Status',
  'ops.deps.col_version': 'Version',
  'ops.deps.col_purpose': 'Purpose',
  'ops.deps.col_install': 'Install Command',
  'ops.deps.installed': 'Installed',
  'ops.deps.missing': 'Missing',
  'ops.models.title': 'Model Files',
  'ops.models.col_name': 'Model',
  'ops.models.col_path': 'Path',
  'ops.models.col_status': 'Status',
  'ops.models.col_download': 'Download Command',
  'ops.models.exists': 'Present',
  'ops.models.missing': 'Missing',
  'ops.hardware.title': 'Hardware Acceleration',
  'ops.hardware.cuda': 'CUDA',
  'ops.hardware.on': 'Available',
  'ops.hardware.off': 'Unavailable',
  'ops.hardware.unknown': 'Unknown (torch not loaded)',
  'ops.hardware.nvidia_smi': 'nvidia-smi',
  'ops.hardware.found': 'Found',
  'ops.hardware.not_found': 'Not found',
  'ops.hardware.platform': 'Platform',

  // DynClassificationPanel (Dynamic Classification)
  'dyn.title': 'Declarative Dynamic Classification (Dynamic Classification)',
  'dyn.subtitle': 'Supports multi-domain, multi-standard (Sichuan/GD/Financial, etc.) YAML configs, ready-to-use matching operators and auto config extraction from spec docs.',
  'dyn.standard.label': 'Current Standard',
  'dyn.standard.loading': 'Loading…',
  'dyn.standard.default': 'Default (Generic Rule Engine)',
  'dyn.standard.generic_hint': 'Using generic rule engine (no standard selected)',
  'dyn.standard.default_level': 'Default level: {0}',
  'dyn.standard.rule_count': 'Rules: {0}',
  'dyn.standard.category_count': 'Categories: {0}',
  'dyn.tab.eval': 'Field Evaluation (Eval)',
  'dyn.tab.record': 'Record Classification (Record)',
  'dyn.tab.generate': 'Auto Generate Config (Auto Generate)',
  'dyn.tab.info': 'Standard/Domain/Operator Directory (Directory)',
  'dyn.tab.validate': 'Rule Validation (Validate)',
  // Eval tab
  'dyn.eval.params_title': 'Evaluation Parameters',
  'dyn.eval.field_name': 'Field Name (fieldName)',
  'dyn.eval.field_value': 'Field Value (value, optional)',
  'dyn.eval.domain': 'Domain Pack (domain, optional)',
  'dyn.eval.domain_ph': 'general-pii / medical',
  'dyn.eval.standard_hint': 'Classification standard controlled by top switcher: {0}',
  'dyn.eval.standard_default': 'Default generic rule engine',
  'dyn.eval.submit': 'Run Dynamic Evaluation',
  'dyn.eval.submitting': 'Evaluating…',
  'dyn.eval.result_title': 'Evaluation Result',
  'dyn.eval.empty': 'Click "Run Dynamic Evaluation" on the left to get results',
  'dyn.eval.error_fallback': 'Evaluation failed',
  // Record tab
  'dyn.record.input_title': 'Record Classification Input',
  'dyn.record.json_label': 'Record JSON (field name → value)',
  'dyn.record.domain': 'Domain (domain, optional)',
  'dyn.record.submit': 'Run Record Classification',
  'dyn.record.submitting': 'Classifying…',
  'dyn.record.result_title': 'Record Classification Result',
  'dyn.record.empty': 'Click "Run Record Classification" on the left to get results',
  'dyn.record.json_error': 'Invalid JSON format, please check input',
  'dyn.record.error_fallback': 'Record classification failed',
  // Generate tab
  'dyn.gen.title': 'One-click YAML Config Extraction from Standard Markdown Spec',
  'dyn.gen.desc': 'Input a local/industry standard spec doc (e.g. Sichuan Health Big Data Guide.md) to auto-detect the grading matrix and extract YAML config files.',
  'dyn.gen.path_label': 'Document Path (docPath)',
  'dyn.gen.submit': 'Auto Generate Full YAML Config',
  'dyn.gen.submitting': 'Parsing & extracting…',
  'dyn.gen.success': 'Generated successfully!',
  'dyn.gen.error_fallback': 'Generation failed',
  // Info tab
  'dyn.info.standards': 'Available Standards',
  'dyn.info.domains': 'Domain Packs',
  'dyn.info.operators': 'Registered Operators',
  'dyn.info.loading': 'Loading…',
  'dyn.info.error_fallback': 'Failed to query system info',
  // Validate tab
  'dyn.validate.title': 'Online Rule YAML Validity Check',
  'dyn.validate.desc': 'Detects operator-not-found errors, syntax errors and fuzzy spell-correction hints.',
  'dyn.validate.resubmit': 'Re-validate',
  'dyn.validate.error_fallback': 'Rule validation failed',
  // Result views
  'dyn.result.final_level': 'Final Sensitivity Level',
  'dyn.result.record_final_level': 'Record-level Final Level',
  'dyn.result.engine_layer': 'Engine layer: {0}',
  'dyn.result.field_count': 'Fields: {0}',
  'dyn.result.confidence': 'Confidence',
  'dyn.result.needs_review': ' · Needs review',
  'dyn.result.reasoning': 'Reasoning: ',
  'dyn.result.hit_tags': 'Hit tags ({0})',
  'dyn.result.suppressed_tags': 'Suppressed tags ({0})',
  'dyn.result.raw_json': 'View Raw JSON',
  'dyn.result.tag.rule': 'Rule:',
  'dyn.result.tag.engine': 'Engine:',
  'dyn.result.tag.match': 'Match:',
  'dyn.result.tag.needs_review': '⚠️ Needs human review',
  'dyn.result.tag.downgrade': '↓ From downgrade',
  'dyn.result.audit.duration': 'Duration',
  'dyn.result.audit.rules_hit': 'Rules hit',
  'dyn.result.audit.standard': 'Standard:',
  'dyn.result.audit.domain': 'Domain:',
  'dyn.result.table.field': 'Field',
  'dyn.result.table.level': 'Level',
  'dyn.result.table.confidence': 'Confidence',
  'dyn.result.table.rules': 'Hit rules',

  // FileTest (Data File Privacy Processing)
  'file.title': 'Data File Privacy Processing',
  'file.subtitle': 'Upload a CSV/JSON file and choose masking / K-anonymity.',
  'file.file_label': 'Data File',
  'file.selected': 'Selected: {0} ({1} KB)',
  'file.sample_prefix': 'Sample files:',
  'file.sample_fill_title': 'Fill {0} sample file and use it directly',
  'file.sample_download_title': 'Download {0} sample file locally',
  'file.op_label': 'Operation Type',
  'file.op_mask': 'Data Masking',
  'file.op_mask_hint': 'Mask specified columns',
  'file.op_kano': 'K-Anonymity',
  'file.op_kano_hint': 'Generalize quasi-identifier columns',
  'file.mask_cols': 'Masking columns (comma-separated)',
  'file.mask_context': 'Context (optional)',
  'file.mask_context_ph': 'e.g. medical scenario',
  'file.qi_cols': 'Quasi-identifier columns QI (comma-separated)',
  'file.k_value': 'K value',
  'file.submitting': 'Processing…',
  'file.submit': 'Upload & Process',
  'file.no_file': 'Please select a CSV or JSON file first',
  'file.err_ext': 'Only .csv and .json files are supported',
  'file.err_size': 'File too large ({0} MB), limit {1} MB',
  'file.original': 'Original Data',
  'file.result': 'Processed Result',
  'file.rows_preview': 'First {0} rows / {1} total',
  'file.changed': 'Changed',
  'file.preview_unavailable': 'Preview unavailable: {0}',
  'file.original_empty': 'Select or fill a sample file to preview original data here',
  'file.result_no_records': 'This response returned no record array; see the raw response JSON above',
  'file.result_empty': 'View results here after processing; changed cells will be highlighted',

  // ErrorBoundary (Error Boundary)
  'error.title': 'UI Render Error',
  'error.unknown': 'An unknown error occurred',
  'error.retry': 'Retry',

  // App
  'app.loading': 'Loading endpoints…',
  'app.connect_failed': 'Cannot connect to backend {0}',
  'app.retry': 'Retry',
};

/** 双语字典映射：语言代码 → 字典 / Bilingual dictionary mapping: language code → dictionary */
const dictionaries: Record<Lang, Record<string, string>> = { zh, en };

/**
 * i18n 上下文值接口 / i18n Context Value Interface
 *
 * 通过 React Context 向下传递的语言服务能力。
 * Language service capabilities passed down via React Context.
 */
export interface I18nContextValue {
  /** 当前语言 / Current language */
  lang: Lang;
  /** 切换语言（同时持久化到 localStorage）/ Switch language (also persists to localStorage) */
  setLang: (l: Lang) => void;
  /** 翻译函数：根据 key 查找当前语言文本，并替换 {0},{1},... 占位符 / Translation function: looks up current language text by key, replaces {0},{1},... placeholders */
  t: (key: string, ...args: (string | number)[]) => string;
}

/**
 * 创建 i18n Context（默认值：中文 + 空操作 + 原样返回 key）
 * Create i18n Context (default: Chinese + noop + return key as-is)
 *
 * 导出 Context 本身，供无法使用 useI18n Hook 的类组件
 * （如 ErrorBoundary）通过 ``static contextType`` 接入。
 * The Context itself is exported so class components that cannot use the
 * useI18n Hook (e.g. ErrorBoundary) can connect via ``static contextType``.
 */
export const I18nContext = createContext<I18nContextValue>({
  lang: 'zh',          // 默认中文 / Default Chinese
  setLang: () => {},   // 空操作（Provider 外调用时无效）/ Noop (ineffective outside Provider)
  t: (key) => key,     // 原样返回 key（Provider 外调用时的回退）/ Return key as-is (fallback outside Provider)
});

/**
 * 获取初始语言偏好 / Get Initial Language Preference
 *
 * 优先从 localStorage 读取，无效时默认中文。
 * Reads from localStorage first, defaults to Chinese when invalid.
 */
function getInitialLang(): Lang {
  try {
    const stored = localStorage.getItem('console-lang'); // 读取存储 / Read stored value
    if (stored === 'zh' || stored === 'en') return stored; // 有效值直接返回 / Return valid value directly
  } catch { /* 忽略 localStorage 不可用（如隐私模式）/ Ignore localStorage unavailable (e.g. private mode) */ }
  return 'zh'; // 默认中文 / Default Chinese
}

/**
 * i18n 提供者组件 / i18n Provider Component
 *
 * 包裹应用根部，向下提供 lang/setLang/t 三个值。
 * Wraps app root, provides lang/setLang/t values downward.
 *
 * @param children - 子组件 / Child components
 */
export function I18nProvider({ children }: { children: ReactNode }) {
  /** 语言状态（初始值从 localStorage 读取）/ Language state (initial value read from localStorage) */
  const [lang, setLangState] = useState<Lang>(getInitialLang);

  /**
   * 切换语言并持久化 / Switch language and persist
   *
   * 使用 useCallback 避免每次渲染创建新函数引用。
   * Uses useCallback to avoid creating new function reference each render.
   */
  const setLang = useCallback((l: Lang) => {
    setLangState(l); // 更新状态 / Update state
    try { localStorage.setItem('console-lang', l); } catch { /* 忽略存储失败 / Ignore storage failure */ }
  }, []);

  /**
   * 翻译函数 / Translation Function
   *
   * 详细逻辑 / Detailed Logic：
   *   1. 从当前语言字典中查找 key 对应的文本，未找到时回退为 key 本身；
   *   2. 遍历 args，将文本中的 {0}, {1}, ... 替换为对应参数值。
   *   1. Looks up key in current language dictionary, falls back to key itself if not found;
   *   2. Iterates args, replaces {0}, {1}, ... in text with corresponding argument values.
   */
  const t = useCallback(
    (key: string, ...args: (string | number)[]) => {
      let text = dictionaries[lang][key] ?? key; // 查找翻译，回退为 key / Look up translation, fallback to key
      args.forEach((arg, i) => {
        text = text.replace(`{${i}}`, String(arg)); // 替换占位符 / Replace placeholder
      });
      return text;
    },
    [lang], // 仅语言变化时重建 / Rebuild only when language changes
  );

  /* 通过 Context.Provider 向下传递语言服务 / Pass language service down via Context.Provider */
  return <I18nContext.Provider value={{ lang, setLang, t }}>{children}</I18nContext.Provider>;
}

/**
 * i18n Hook：在任意组件中获取翻译服务 / i18n Hook: Get translation service in any component
 *
 * @returns { lang, setLang, t } 语言状态、切换函数、翻译函数 / Language state, switch function, translation function
 */
export function useI18n() {
  return useContext(I18nContext); // 读取最近的 I18nProvider / Read nearest I18nProvider
}
