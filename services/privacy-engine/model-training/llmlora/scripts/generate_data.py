#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
训练数据自动生成与蒸馏脚本（规则驱动版） / Rule-driven SFT data generation.

数据管道完全基于 PrivShield 的 Layer-1 可配置规则引擎：
The pipeline is fully grounded in the project's Layer-1 configurable rule engine:

1. Faker 伪造工厂 + 领域模板合成含敏感实体的文本 /
   Faker + domain templates synthesize texts containing sensitive entities.
2. ConfigurableRuleEngine（general-pii + medical，default L1~L5 体系）
   对每个实体求值，得到规则裁定的 level/category 作为 Ground Truth /
   Each entity value is evaluated by the rule engine, whose verdict
   (level/category) becomes the ground-truth label.
3. 规则化无痕抹平：实体 span 按类别替换为占位符并做标点清洗 /
   Rule-based smoothing replaces entity spans with placeholders and
   cleans punctuation artifacts.
4. Zero-Leakage 双重校验：敏感值残留检查 + 规则引擎复扫，不合格即丢弃 /
   Zero-leakage double check (literal residual scan + rule-engine rescan);
   failing samples are discarded.

用法 / Usage:
    python -m llmlora.scripts.generate_data --train-size 1000 --dev-size 100 --test-size 50
"""
from __future__ import annotations

import argparse
import json
import random
import re
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

from faker import Faker

# 保证从任意工作目录运行时都能导入 llmlora 与 PrivShield
# Ensure llmlora and PrivShield are importable from any cwd
_LLMLORA_DIR = Path(__file__).resolve().parents[1]
_MODEL_TRAINING_DIR = _LLMLORA_DIR.parent
_ENGINE_DIR = _MODEL_TRAINING_DIR.parent
_REPO_ROOT = _ENGINE_DIR.parent.parent

for _p in (_MODEL_TRAINING_DIR, _REPO_ROOT):
    if str(_p) not in sys.path:
        sys.path.insert(0, str(_p))

from llmlora.src.utils.metrics import find_leaked_values  # noqa: E402

# 规则引擎依赖（项目主包） / Rule engine dependencies from the main project package
from engine.dynclassification.engine import (  # noqa: E402
    ConfigurableRuleEngine,
)
from engine.dynclassification.profile_loader import (  # noqa: E402
    ProfileLoader,
)

# 参与数据打标的领域规则包（必须使用 default L1~L5 体系；
# Domain packs used for labeling (must share the default L1~L5 taxonomy;
# finance 包使用 C2~C4 体系，禁止混入）
# the finance pack uses C2~C4 levels and must NOT be mixed in)
LABELING_DOMAINS = ["general-pii", "medical"]

# 无实体样本的默认密级 / Default level for entity-free samples
NEGATIVE_LEVEL = "L1"

# 规则未命中时的兜底等级/类别（按实体类别） / Fallback level/category per entity kind
FALLBACK_LABELS: Dict[str, Tuple[str, str]] = {
    "NAME": ("L3", "PERSONAL_BASIC"),
    "ID_CARD": ("L3", "PERSONAL_BASIC"),
    "PHONE": ("L3", "PERSONAL_BASIC"),
    "BANK_CARD": ("L3", "PERSONAL_BASIC"),
    "EMAIL": ("L3", "PERSONAL_BASIC"),
    "AGE": ("L2", "PERSONAL_BASIC"),
    "MEDICAL_DIAGNOSIS": ("L4", "MEDICAL_TREATMENT"),
}

# 规则引擎字段名提示词：触发 field_name 关键词类规则
FIELD_HINTS: Dict[str, str] = {
    "NAME": "patient_name",
    "ID_CARD": "id_card",
    "PHONE": "mobile",
    "BANK_CARD": "bank_card",
    "EMAIL": "email",
    "AGE": "patient_age",
    "MEDICAL_DIAGNOSIS": "clinical_diagnosis",
}

# 抹平占位符（按实体类别）：采用统一且清晰的合规占位词
# Smoothing placeholders per entity kind: using clear, bracketed context-rewriting tokens
MASK_TOKENS: Dict[str, List[str]] = {
    "NAME": ["[相关姓名已抹平]", "[姓名已打码]", "[姓名已做脱敏处理]"],
    "ID_CARD": ["[身份证号已抹平]", "[身份证已打码]", "[身份证号已合规抹平]"],
    "PHONE": ["[联系电话已抹平]", "[手机号已打码]", "[联系电话已隐去]"],
    "BANK_CARD": ["[银行卡号已抹平]", "[还款账户已打码]", "[银行卡号已合规抹平]"],
    "EMAIL": ["[电子邮箱已抹平]", "[邮箱已打码]", "[电子邮箱已隐去]"],
    "MEDICAL_DIAGNOSIS": ["[诊断信息已抹平]", "[处方/诊断已打码]", "[诊疗与处方已做合规抹平]"],
}

# 丰富的多领域自然语言模板：覆盖医疗、金融、企业人事、电商客服与公共资讯
# Rich multi-domain templates covering medical, finance, enterprise HR, e-commerce, and public news
TEMPLATES: Dict[str, List[str]] = {
    "finance": [
        "客户{name}（身份证：{id_card}）申请提现{amount}元到卡号{bank_card}。",
        "用户{name}的贷款申请已审批通过，绑定还款账户{bank_card}，联系电话{phone}。",
        "交易流水：卡号{bank_card}于{date}消费{amount}元，商户：{merchant}。",
        "理赔结算通知：保单被保险人{name}（身份证{id_card}）理赔申请已核准，赔付款{amount}元已打入卡号{bank_card}，联系电话{phone}。",
        "证券开户确认：客户{name}预留联系电话{phone}，绑定资金托管银行卡号{bank_card}，电子邮箱{email}。",
    ],
    "medical": [
        "患者{name}，性别{gender}，{age}岁，诊断为{disease}，开具处方{medication}，联系电话{phone}。",
        "住院病历：患者{name}（身份证{id_card}），主诉{symptom}，检查项目{exam}。",
        "检验报告：患者{name}的{exam_item}结果为{result}，参考范围{reference}。",
        "门诊复诊记录：患者{name}（身份证{id_card}），主诉{symptom}，临床初步诊断为{disease}，医生开具处方{medication}，留存电话{phone}。",
        "处方配药通知：患者{name}的处方药{medication}已配齐，请凭身份证号{id_card}前往药房窗口领取，如有疑问请致电{phone}。",
    ],
    "enterprise": [
        "员工{name}的绩效评估已生成，邮箱：{email}，薪资：{salary}元/月。",
        "人事部通知：{name}（身份证{id_card}）将于{date}入职，联系电话{phone}。",
        "背景调查审核：候选人{name}的背调信息已确认，预留电子邮箱{email}，紧急联系电话{phone}。",
        "差旅报销申请：员工{name}申请报销{date}出差费用共计{amount}元，打款卡号{bank_card}。",
    ],
    "ecommerce": [
        "售后退款申请：买家{name}对订单发起退款，退款金额{amount}元，原路退回至卡号{bank_card}，联系电话{phone}。",
        "物流配送变更：用户{name}修改了收货联系电话为{phone}，预留电子邮箱{email}。",
    ],
    "negative": [
        "今日天气晴朗，适合户外活动。建议市民注意防晒，多补充水分。",
        "根据最新研究报告，全球芯片市场规模预计将在未来三年持续扩大。",
        "本次季度会议讨论了产品路线图、技术架构升级以及团队建设三个议题。",
        "图书馆新到一批科普读物，欢迎读者前来借阅，开放时间保持不变。",
        "系统维护公告：本周六凌晨2:00至4:00将进行数据库版本升级，届时服务可能暂停访问。",
        "分布式缓存集群上线了最新的LRU淘汰策略，有效降低了内存峰值使用率。",
    ],
}

DISEASES = [
    # 常规慢病（L1/L2，原样保留）
    "急性支气管炎", "II型糖尿病", "原发性高血压病3级", "慢性胃炎",
    # 消化道恶性肿瘤（L4，降级为消化道慢性疾病）
    "胃癌", "结肠癌", "食管癌",
    # 呼吸系统恶性肿瘤（L4，降级为呼吸系统常规疾病）
    "肺癌", "支气管肺癌",
    # 心血管重症（L4，降级为心血管常见病）
    "急性心肌梗死", "冠状动脉重度狭窄", "心力衰竭",
    # 肝脏器官重症（L4，降级为肝脏常见病变）
    "肝硬化失代偿期", "病毒性肝炎",
    # 罕见遗传缺陷（L5，降级为遗传性神经系统疾病）
    "亨廷顿舞蹈病",
    # 性病 / 极敏感传染病（L4/L5，彻底抹平隐去）
    "梅毒", "淋病", "HIV抗体阳性", "尖锐湿疣", "艾滋病",
    # 重度精神障碍（L4，彻底抹平隐去）
    "重度抑郁症", "精神分裂症", "双相情感障碍",
]

MEDICATIONS = [
    "阿莫西林克拉维酸钾", "二甲双胍片", "硝苯地平控释片", "舍曲林片",
    "拉米夫定片", "阿昔洛韦片", "奥氮平片", "富马酸替诺福韦二吡呋酯片"
]

SYMPTOMS = [
    "持续性头痛", "胸闷气短", "反复发热", "关节肿痛",
    "外阴赘生物", "无痛性溃疡", "命令性幻听", "舞蹈样动作", "不洁性接触史"
]

EXAMS = [
    "胸部CT", "血常规", "肝功能", "心电图",
    "醋酸白试验阳性", "TPPA阳性", "RPR阳性", "CD4+计数检查", "HBV-DNA载量"
]
MERCHANTS = ["京东商城", "美团外卖", "滴滴出行", "支付宝转账"]

# 领域采样权重 / Domain sampling weights
DOMAIN_WEIGHTS: List[Tuple[str, int]] = [
    ("finance", 20),
    ("medical", 40),
    ("enterprise", 15),
    ("ecommerce", 10),
    ("negative", 15),
]

# 抹平后标点清洗规则 / Punctuation cleanup rules after smoothing
_PUNCT_CLEANUP_RULES = [
    (re.compile(r"（\s*）"), ""),
    (re.compile(r"\(\s*\)"), ""),
    (re.compile(r"：\s*[，。]"), "："),
    (re.compile(r"，{2,}"), "，"),
    (re.compile(r"。{2,}"), "。"),
    (re.compile(r"、{2,}"), "、"),
]


class RuleBasedDataGenerator:
    """规则驱动的 SFT 样本生成器 / Rule-driven SFT sample generator."""

    def __init__(self, rules_dir: str, seed: int = 42):
        """初始化 Faker 与规则引擎 / Initialize Faker and the rule engine."""
        self.rng = random.Random(seed)
        self.faker = Faker("zh_CN")
        Faker.seed(seed)

        loader = ProfileLoader(rules_dir)
        taxonomy = loader.load_taxonomy("default")
        profiles = [loader.load_profile(d) for d in LABELING_DOMAINS]
        self.engine = ConfigurableRuleEngine(
            taxonomy=taxonomy,
            profiles=profiles,
            domain="llmlora-data",
        )
        self.taxonomy = taxonomy
        self.dropped = 0
        self.level_stats: Dict[str, int] = {}
        # 结构签名（模板 + 非标识符槽位值）跨分割累积集合：用于 dev/test
        # 排除与 train 近重复（同模板同病种仅 PII 不同的样本）。
        self._seen_signatures: set = set()
        self.near_duplicate_dropped = 0
        # 跨分割 input 去重集合：防止 train/dev/test 出现完全相同的敏感样本（数据泄漏）
        # Cross-split input dedup set: prevents identical sensitive samples
        # leaking across train/dev/test splits
        self._seen_inputs: set[str] = set()
        self.duplicate_dropped = 0

    def _rank(self, level: str) -> int:
        """获取等级排序权重（未知等级记 0） / Get level rank (unknown = 0)."""
        level_def = self.taxonomy.levels.get(level)
        return level_def.rank if level_def else 0

    def label_entity(self, kind: str, value: str) -> Tuple[str, str, float]:
        """用规则引擎裁定单个实体的 (level, category, confidence)。"""
        field_hint = FIELD_HINTS.get(kind, kind.lower())
        tags, _suppressed = self.engine.evaluate(field_hint, value)
        if tags:
            best = max(tags, key=lambda t: self._rank(t.level))
            return best.level, best.category, 1.0
        level, category = FALLBACK_LABELS.get(kind, ("L3", "PERSONAL_BASIC"))
        return level, category, 0.8

    # ------------------------------------------------------------------
    # 规则化无痕抹平与四柱强剥离 / Rule-based smoothing & Four-pillar stripping
    # ------------------------------------------------------------------

    def _mask_value_by_kind(self, kind: str, val: str, level: str) -> str:
        """根据 医疗健康数据分类分级与隐私脱敏算法标准规范.md 执行严格的四柱剥离与泛化降级：
        
        1. 极敏感传染病/性病 (梅毒, HIV, 尖锐湿疣) 与 重度精神障碍 (抑郁, 精神分裂)：彻底隐去/重写为常规门诊随访；
        2. 恶性肿瘤 / 罕见遗传病 / 心血管重症：执行分类范畴降级（如 胃癌/结肠癌 -> 消化道慢性疾病；亨廷顿舞蹈病 -> 遗传性神经系统疾病）；
        3. 四柱体征/检查/处置（醋酸白阳性、CD4+、外阴赘生物、CO2激光）：整词/整句清除；
        4. PII 标识符：平滑无痕替代（患者、相关身份证号、预留联系电话）。
        """
        # AGE K-匿名泛化处理 (§6.4)：无论密级一律先泛化，保证原始年龄不原样残留。
        # 注意：该分支必须位于 L1/L2 提前返回之前，否则永远不可达，
        # 且原样残留的年龄会被 verify_zero_leakage 字面检查命中导致样本全灭。
        if kind == "AGE":
            try:
                age_val = int(val)
                if age_val < 60:
                    gen_age = age_val - (age_val % 3)
                else:
                    gen_age = age_val - (age_val % 2)
                return str(gen_age)
            except ValueError:
                return val

        if level in ("L1", "L2"):
            return val

        if kind == "MEDICAL_DIAGNOSIS" or level in ("L4", "L5"):
            # §6.5 禁止泛化范畴（性病 / HIV / 重度精神障碍）：彻底抹平
            if any(s in val for s in ["梅毒", "淋病", "HIV", "艾滋", "尖锐湿疣", "醋酸白", "TPPA", "RPR", "CD4+"]):
                return "门诊常规检查"
            if any(s in val for s in ["抑郁", "精神分裂", "狂躁", "双相", "幻听"]):
                return "门诊随访"

            # §6.5 范畴化降级泛化映射
            if any(s in val for s in ["癌", "瘤", "白血病", "淋巴瘤"]):
                if any(k in val for k in ["胃", "肠", "食管", "消化"]):
                    return "消化道慢性疾病"
                if any(k in val for k in ["肺", "支气管"]):
                    return "呼吸系统常规疾病"
                if "肝" in val:
                    return "肝脏常见病变"
                return "常规慢性疾病"

            if "亨廷顿舞蹈病" in val or "舞蹈样动作" in val:
                return "遗传性神经系统疾病"

            if any(s in val for s in ["心肌梗死", "冠心病", "狭窄", "心衰"]):
                return "心血管系统常见病"

            if "肝硬化" in val or "肝炎" in val:
                return "肝脏常见病变"

            if any(med in val for med in ["片", "胶囊", "颗粒", "注射液", "口服液"]):
                return "常规门诊处方药"

            return "常见慢性疾病"

        # PII 实体掩码 (§6.2)
        if kind == "NAME":
            if len(val) <= 1:
                return "*"
            if len(val) == 2:
                return val[0] + "*"
            if len(val) == 3:
                return val[0] + "*" + val[-1]
            return val[:2] + "*" * (len(val) - 2)

        if kind == "ID_CARD":
            if len(val) == 18:
                return val[:6] + "*" * 8 + val[14:]
            if len(val) == 15:
                return val[:6] + "*" * 6 + val[12:]
            return val[:3] + "*" * max(1, len(val) - 6) + val[-3:] if len(val) > 6 else "*" * len(val)

        if kind == "PHONE":
            if len(val) == 11:
                return val[:3] + "****" + val[7:]
            return val[:3] + "*" * max(1, len(val) - 5) + val[-2:] if len(val) > 5 else "*" * len(val)

        if kind == "BANK_CARD":
            if len(val) >= 12:
                return val[:4] + "*" * (len(val) - 8) + val[-4:]
            return val[:4] + "*" * (len(val) - 4) if len(val) > 4 else "*" * len(val)

        if kind == "EMAIL":
            if "@" in val:
                user, domain = val.split("@", 1)
                if len(user) <= 2:
                    masked_user = user[0] + "*"
                else:
                    masked_user = user[0] + "*" * (len(user) - 2) + user[-1]
                return f"{masked_user}@{domain}"
            return "*" * len(val)

        return val

    def smooth_text(self, text: str, entities: List[Dict[str, Any]]) -> str:
        """根据密级分层执行无痕重写与高敏语义泛化降级。"""
        smoothed = text
        replacements: List[Tuple[str, str]] = []
        for entity in entities:
            value = entity["_value"]
            kind = entity["_kind"]
            level = entity.get("level", "L3")
            token = self._mask_value_by_kind(kind, value, level)
            replacements.append((value, token))

        for value, token in replacements:
            smoothed = smoothed.replace(value, token)

        for pattern, repl in _PUNCT_CLEANUP_RULES:
            smoothed = pattern.sub(repl, smoothed)
        return smoothed

    # ------------------------------------------------------------------
    # Zero-Leakage QA / Zero-leakage QA
    # ------------------------------------------------------------------

    def verify_zero_leakage(
        self, smoothed: str, entities: List[Dict[str, Any]]
    ) -> bool:
        """双重零泄漏校验 / Dual zero-leakage verification."""
        values = [e["_value"] for e in entities]
        if find_leaked_values(smoothed, values):
            return False
        rescan_tags, _ = self.engine.evaluate("content", smoothed)
        return all(self._rank(t.level) < 2 for t in rescan_tags)

    # ------------------------------------------------------------------
    # 样本合成 / Sample synthesis
    # ------------------------------------------------------------------

    def _slot_values(self) -> Dict[str, str]:
        """生成一组 Faker 伪造槽位值 / Generate one set of Faker slot values."""
        return {
            "name": self.faker.name(),
            "id_card": self.faker.ssn(),
            "phone": self.faker.phone_number(),
            "bank_card": self.faker.credit_card_number(),
            "amount": str(self.rng.randint(1000, 50000)),
            "email": self.faker.email(),
            "salary": str(self.rng.randint(8000, 40000)),
            "gender": self.rng.choice(["男", "女"]),
            "age": str(self.rng.randint(18, 80)),
            "disease": self.rng.choice(DISEASES),
            "medication": self.rng.choice(MEDICATIONS),
            "symptom": self.rng.choice(SYMPTOMS),
            "exam": self.rng.choice(EXAMS),
            "date": self.faker.date(),
            "merchant": self.rng.choice(MERCHANTS),
            "exam_item": self.rng.choice(["血糖", "血压", "胆固醇"]),
            "result": self.rng.choice(["偏高", "正常", "偏低"]),
            "reference": self.rng.choice(["3.9-6.1mmol/L", "90-140mmHg"]),
        }

    # 槽位名 -> 实体类别 / Slot name to entity category
    _SLOT_CATEGORY = {
        "name": "NAME",
        "id_card": "ID_CARD",
        "phone": "PHONE",
        "bank_card": "BANK_CARD",
        "email": "EMAIL",
        "age": "AGE",
        "disease": "MEDICAL_DIAGNOSIS",
        "medication": "MEDICAL_DIAGNOSIS",
        "symptom": "MEDICAL_DIAGNOSIS",
        "exam": "MEDICAL_DIAGNOSIS",
    }

    # PII 标识符槽位：不参与结构签名（同模板同语义槽位、仅 PII 不同的样本视为近重复）
    _IDENTIFIER_SLOTS = frozenset({"name", "id_card", "phone", "bank_card", "email"})

    def generate_one(self) -> Optional[Dict[str, Any]]:
        """生成一条精简版 SFT 样本（包含 final_level, confidence, reasoning, sanitized_text；无 entities）"""
        domains = [d for d, _ in DOMAIN_WEIGHTS]
        weights = [w for _, w in DOMAIN_WEIGHTS]
        domain = self.rng.choices(domains, weights=weights, k=1)[0]
        template = self.rng.choice(TEMPLATES[domain])

        # 负样本：无敏感实体，抹平文本即原文
        if domain == "negative":
            output_payload = {
                "final_level": NEGATIVE_LEVEL,
                "confidence": 1.0,
                "reasoning": "文本为公开通用资讯，无敏感信息",
                "sanitized_text": template,
            }
            self.level_stats[NEGATIVE_LEVEL] = self.level_stats.get(NEGATIVE_LEVEL, 0) + 1
            return {
                "input": template,
                "output": json.dumps(output_payload, ensure_ascii=False),
            }

        values = self._slot_values()
        input_text = template.format(**values)

        # 结构签名 = (领域, 模板, 决定标签的语义槽位值)：同模板且实体槽位（病种/药品/
        # 症状/检查/年龄）相同、仅 PII 标识符（姓名/证件号等）或日期/金额/商户等
        # 非实体槽位不同的样本视为近重复，供 dev/test 分割排除。
        sig_slots = tuple(
            sorted(
                (slot, val)
                for slot, val in values.items()
                if slot in self._SLOT_CATEGORY
                and slot not in self._IDENTIFIER_SLOTS
                and f"{{{slot}}}" in template
            )
        )
        sample_sig = (domain, template, sig_slots)

        entities: List[Dict[str, Any]] = []
        for slot, category in self._SLOT_CATEGORY.items():
            if f"{{{slot}}}" not in template:
                continue
            value = values[slot]
            level, rule_category, confidence = self.label_entity(category, value)
            entities.append(
                {
                    "text": value,
                    "category": rule_category,
                    "level": level,
                    "confidence": confidence,
                    "_kind": category,
                    "_value": value,
                }
            )

        smoothed = self.smooth_text(input_text, entities)
        if not self.verify_zero_leakage(smoothed, entities):
            self.dropped += 1
            return None

        # 密级取 rank 最高的实体；confidence 直接采用该实体 label_entity 的
        # 实际返回值（规则命中 1.0 / 兜底 0.8），不再硬编码 0.95
        max_entity = max(entities, key=lambda e: self._rank(e["level"]), default=None)
        max_level = max_entity["level"] if max_entity else NEGATIVE_LEVEL
        confidence = max_entity["confidence"] if max_entity else 1.0
        self.level_stats[max_level] = self.level_stats.get(max_level, 0) + 1

        output_payload = {
            "final_level": max_level,
            "confidence": confidence,
            "reasoning": f"命中{max_level}敏感分类特征，已进行无痕重写与降级抹平",
            "sanitized_text": smoothed,
        }
        return {
            "input": input_text,
            "output": json.dumps(output_payload, ensure_ascii=False),
            "_sig": sample_sig,  # 结构签名，generate_batch 使用后弹出，不写入 jsonl
        }

    def generate_batch(self, count: int, exclude_prior_signatures: bool = False) -> List[Dict[str, Any]]:
        """生成 count 条样本（QA 丢弃与跨分割重复均自动补采）。

        Generate samples, resampling on QA drops and cross-split duplicates.

        去重策略 / Dedup policy:
        - 以 input 全文为键，跨 train/dev/test 累积去重（同一分割内同样生效），
          防止完全相同的敏感样本泄漏到多个分割；
        - exclude_prior_signatures=True 时（dev/test 分割）额外按结构签名
          （模板 + 非标识符槽位值）去重：同模板同病种仅 PII 不同的近重复样本
          不会进入评估集，避免评估指标虚高；
        - 负样本（negative 领域，固定公开模板、无任何敏感实体）不参与去重，
          否则少量模板耗尽后会撞满 max_attempts 并破坏 L1 类分布；
          负样本跨分割重复不泄漏敏感信息。
        """
        samples: List[Dict[str, Any]] = []
        # 补采上限防止病态循环；签名排除模式下签名空间更小，放宽补采倍数
        attempts = 0
        max_attempts = count * (10 if exclude_prior_signatures else 5)
        negative_templates = set(TEMPLATES["negative"])
        while len(samples) < count and attempts < max_attempts:
            attempts += 1
            sample = self.generate_one()
            if sample is None:
                continue
            input_text = sample["input"]
            if input_text in negative_templates:
                samples.append(sample)
                continue
            sig = sample.pop("_sig", None)
            if input_text in self._seen_inputs:
                self.duplicate_dropped += 1
                continue
            if exclude_prior_signatures and sig is not None and sig in self._seen_signatures:
                self.near_duplicate_dropped += 1
                continue
            self._seen_inputs.add(input_text)
            if sig is not None:
                self._seen_signatures.add(sig)
            samples.append(sample)
        return samples


def _write_jsonl(path: Path, samples: List[Dict[str, Any]]) -> None:
    """写出 JSONL 文件 / Write a JSONL file."""
    with open(path, "w", encoding="utf-8") as f:
        for sample in samples:
            f.write(json.dumps(sample, ensure_ascii=False) + "\n")


def main() -> None:
    """数据生成入口 / Data generation entry point."""
    parser = argparse.ArgumentParser(description="生成 llmlora 规则驱动 SFT 数据集")
    parser.add_argument("--train-size", type=int, default=30000, help="训练集数量")
    parser.add_argument("--dev-size", type=int, default=1000, help="验证集数量")
    parser.add_argument("--test-size", type=int, default=500, help="测试集数量")
    parser.add_argument(
        "--output-dir",
        type=str,
        default=str(_LLMLORA_DIR / "data"),
        help="数据导出目录",
    )
    parser.add_argument(
        "--rules-dir",
        type=str,
        default=str(_ENGINE_DIR / "rules" if (_ENGINE_DIR / "rules").exists() else _REPO_ROOT / "rules"),
        help="项目规则库目录",
    )
    parser.add_argument("--seed", type=int, default=42, help="随机种子")
    args = parser.parse_args()

    generator = RuleBasedDataGenerator(rules_dir=args.rules_dir, seed=args.seed)
    print(
        f"规则引擎初始化完成：{generator.engine.rule_count} 条普通规则 + "
        f"{generator.engine.downgrade_rule_count} 条降级规则"
    )

    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    for filename, size in [
        ("train.jsonl", args.train_size),
        ("dev.jsonl", args.dev_size),
        ("test.jsonl", args.test_size),
    ]:
        # 评估分割（dev/test）按结构签名排除与先行分割的近重复样本
        is_eval_split = filename != "train.jsonl"
        samples = generator.generate_batch(size, exclude_prior_signatures=is_eval_split)
        _write_jsonl(output_dir / filename, samples)
        print(f"成功导出 {len(samples)}/{size} 条数据到: {output_dir / filename}")

    print(f"零泄漏 QA 丢弃样本数: {generator.dropped}")
    print(f"跨分割/同分割重复丢弃样本数: {generator.duplicate_dropped}")
    print(f"近重复（同模板同语义槽位）丢弃样本数: {generator.near_duplicate_dropped}")
    print(f"密级分布: {dict(sorted(generator.level_stats.items()))}")


if __name__ == "__main__":
    main()
