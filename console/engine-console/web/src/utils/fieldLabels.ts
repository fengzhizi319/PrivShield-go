/**
 * 医疗与医保数据字段中英文映射工具 / Medical & Yibao Field Label Mapping Utilities
 *
 * 为前端表格与分级报告提供 "英文Key (中文字段)" 格式的联合展示名。
 */

export const FIELD_CN_NAMES: Record<string, string> = {
  // === 27 字段康养医疗数据 (kangyang.csv) ===
  name: '姓名',
  id_card_no: '身份证号',
  registered_address: '户籍地址',
  disability_cert_no: '残疾证号',
  medical_insurance_no: '医保证号',
  gender: '性别',
  age: '年龄',
  diagnosis_name: '诊断名称',
  chief_complaint: '主诉',
  present_illness: '现病史',
  past_history: '既往史',
  personal_history: '个人史',
  is_smoking: '是否吸烟',
  smoking_duration: '吸烟时长',
  family_history: '家族史',
  allergic_history: '过敏史',
  department: '科室',
  height: '身高',
  weight: '体重',
  disability_category: '残疾类别',
  disability_level: '残疾等级',
  assess_type_name: '评估类型',
  assess_result_name: '评估结果',
  assess_score: '评估分数',
  assess_time: '评估时间',
  progress_note: '病程记录',
  progress_note_time: '病程记录时间',

  // === 18 字段医保结算数据 (yibao.csv) ===
  insurance_settlement_id: '医保结算流水号',
  person_id: '人员唯一标识',
  birth_date: '出生日期',
  admission_date: '入院日期',
  discharge_date: '出院日期',
  length_of_stay: '住院天数',
  admission_dept: '入院科室',
  discharge_dept: '出院科室',
  hospital_code: '定点医疗机构编码',
  medical_category: '医疗类别',
  discharge_mode: '离院方式',
  settlement_seq_no: '明细结算流水号',
  diagnosis_seq: '诊断序号',
  diagnosis_type: '诊断类别',
  icd10_code: '诊断编码(ICD-10)',
  admission_condition: '入院病情',
};

/**
 * 获取保留英文 Key 并附加中文注解的展示名称。
 * 例如：id_card_no -> "id_card_no (身份证号)"
 */
export function getFieldDisplayName(key: string): string {
  if (!key) return key;
  const cn = FIELD_CN_NAMES[key];
  if (cn) {
    return `${key} (${cn})`;
  }
  return key;
}
