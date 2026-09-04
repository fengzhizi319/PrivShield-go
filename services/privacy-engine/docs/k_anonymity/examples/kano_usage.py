"""K-匿名（K-Anonymity）模块使用示例。

运行方式：
    source .venv/bin/activate
    PYTHONPATH=. python docs/k_anonymity/examples/kano_usage.py
"""

from engine.privacy.kano import (
    BUILTIN_HIERARCHIES,
    anonymize_record,
)
from engine.privacy.kano_table import k_anonymize_table


def demo_record_level() -> None:
    """演示单条记录的启发式 K-匿名泛化。"""
    print("=" * 60)
    print("1. 单记录 K-匿名泛化")
    print("=" * 60)

    record = {
        "name": "张三",
        "age": "28",
        "zipcode": "518057",
        "gender": "女",
        "disease": "胃癌",
    }
    qi_cols = ["age", "zipcode", "gender"]

    for k in [5, 12, 25]:
        result = anonymize_record(record, qi_cols, BUILTIN_HIERARCHIES, k)
        print(f"\nk={k} 泛化结果:")
        print(f"  age    : {result['age']}  (原始: {record['age']})")
        print(f"  zipcode: {result['zipcode']}  (原始: {record['zipcode']})")
        print(f"  gender : {result['gender']}  (原始: {record['gender']})")
        print(f"  disease: {result['disease']}  (敏感字段保持不变)")


def demo_table_level() -> None:
    """演示整张表的 Mondrian K-匿名泛化。"""
    print("\n" + "=" * 60)
    print("2. 数据集级 K-匿名泛化 (Mondrian)")
    print("=" * 60)

    rows = [
        {"age": 25, "zipcode": "100001", "gender": "M", "disease": "A"},
        {"age": 26, "zipcode": "100002", "gender": "M", "disease": "B"},
        {"age": 27, "zipcode": "100003", "gender": "M", "disease": "C"},
        {"age": 55, "zipcode": "200001", "gender": "F", "disease": "D"},
        {"age": 56, "zipcode": "200002", "gender": "F", "disease": "E"},
        {"age": 57, "zipcode": "200003", "gender": "F", "disease": "F"},
    ]
    qi_cols = ["age", "zipcode", "gender"]

    result = k_anonymize_table(rows, qi_cols, k=3, max_depth=10)

    print(f"\n原始记录数: {len(rows)}, 泛化后记录数: {len(result)}")
    print("\n泛化结果（敏感字段 disease 保持不变）:")
    for i, row in enumerate(result, 1):
        print(
            f"  {i}. age={row['age']}, zipcode={row['zipcode']}, "
            f"gender={row['gender']}, disease={row['disease']}"
        )

    # 验证每个等价组大小 >= k
    from collections import Counter

    groups = Counter(
        (str(row["age"]), str(row["zipcode"]), str(row["gender"]))
        for row in result
    )
    print(f"\n等价组大小统计: {dict(groups)}")
    assert all(c >= 3 for c in groups.values()), "存在不满足 K-匿名的等价组"
    print("✓ 所有等价组大小均 >= k=3")


def main() -> None:
    demo_record_level()
    demo_table_level()


if __name__ == "__main__":
    main()
