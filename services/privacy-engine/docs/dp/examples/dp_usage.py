"""差分隐私（DP）模块使用示例。

运行方式：
    source .venv/bin/activate
    PYTHONPATH=. python docs/dp/examples/dp_usage.py
"""

from engine.privacy.budget import default_registry
from engine.privacy.dp import DPApi


def main():
    # 1. 初始化 DPApi，使用独立命名空间管理预算
    dp = DPApi(namespace="dp_usage_demo")

    # 2. 计数查询：统计高血压患者数量
    patients = [1, 0, 1, 1, 0, 1, 0, 0, 1, 1]
    noisy_count = dp.count(patients, epsilon=1.0, mechanism="laplace")
    print(f"真实计数: {sum(patients)}")
    print(f"带噪声计数 (Laplace): {noisy_count:.2f}\n")

    # 3. 求和查询：医疗费用总和，先截断到 [0, 100000]
    charges = [1200.0, 5800.0, 300.0, 99999.0, 15000.0]
    true_sum = sum(min(max(v, 0.0), 100000.0) for v in charges)
    noisy_sum = dp.sum(
        charges,
        epsilon=1.0,
        mechanism="laplace",
        clip_lower=0.0,
        clip_upper=100000.0,
    )
    print(f"真实求和 (已截断): {true_sum:.2f}")
    print(f"带噪声求和 (Laplace): {noisy_sum:.2f}\n")

    # 4. 均值查询：使用 Gaussian 机制
    # 样本量越大，noisy_count 越稳定，均值结果越可靠
    ages = [
        25.0, 34.0, 45.0, 52.0, 29.0, 61.0,
        38.0, 41.0, 27.0, 55.0, 33.0, 48.0,
        36.0, 50.0, 31.0, 44.0, 39.0, 58.0,
    ]
    true_mean = sum(ages) / len(ages)
    noisy_mean = dp.mean(
        ages,
        epsilon=2.0,
        delta=1e-6,
        mechanism="gaussian",
        clip_lower=0.0,
        clip_upper=120.0,
    )
    print(f"真实均值: {true_mean:.2f}")
    print(f"带噪声均值 (Gaussian): {noisy_mean:.2f}\n")

    # 5. 预算管理示例（推荐通过注册表显式获取/创建）
    accountant = default_registry.get_or_create(
        namespace="monthly_report", epsilon_total=4.0, delta_total=1e-5
    )

    accountant.spend(1.0, 1e-6)
    accountant.spend(1.0, 1e-6)

    print(f"总 epsilon: {accountant.epsilon_total}")
    print(f"已用 epsilon: {accountant.epsilon_spent}")
    print(f"剩余 epsilon: {accountant.epsilon_total - accountant.epsilon_spent}")
    print(f"总 delta: {accountant.delta_total}")
    print(f"已用 delta: {accountant.delta_spent}")
    print(f"剩余 delta: {accountant.delta_total - accountant.delta_spent}\n")

    # 6. 高级 API 演示
    print("--- 6. 高级 API 演示 ---")
    dp_adv = DPApi(namespace="dp_adv_demo")
    # 自适应 Clip
    lower, upper = dp_adv.adaptive_clip(charges, epsilon=0.5)
    print(f"自适应搜索 clip_upper: {upper:.2f}")

    # 高维向量 DP-SGD 加噪
    import numpy as np

    grads = np.array([[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])
    noisy_grads = dp_adv.vector_sum(grads, max_norm=5.0, epsilon=1.0, delta=1e-4)
    print(f"DP 向量加噪结果: {noisy_grads}\n")


if __name__ == "__main__":
    main()
