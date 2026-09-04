"""本地差分隐私（Local DP）使用示例。

运行方式：
    source .venv/bin/activate
    PYTHONPATH=. python docs/dp/examples/local_dp_usage.py

说明：
- 本地 DP 中，每个用户在数据离开设备前就完成扰动。
- 服务器只收到扰动后的值，无法反推单个用户的真实值。
- 代价：相同 ε 下，本地 DP 的统计误差通常大于中心式 DP。
"""

from engine.privacy.dp import LocalDPApi


def main():
    api = LocalDPApi(seed=42)

    # 1. 二值随机响应示例
    # 场景：1000 名用户中，真实有 30% 的人患有某疾病
    n = 2000
    true_prevalence = 0.30
    true_values = [1 if i < n * true_prevalence else 0 for i in range(n)]

    # 每个用户本地扰动自己的答案
    reported = api.perturb_binary_batch(true_values, epsilon=1.0)

    # 服务器对扰动后的结果进行纠偏估计
    estimated = api.estimate_binary_frequency(reported, epsilon=1.0)
    print(f"二值随机响应")
    print(f"  真实患病率: {true_prevalence:.2%}")
    print(f"  扰动后 1 的比例: {sum(reported)/n:.2%}")
    print(f"  纠偏估计患病率: {estimated:.2%}\n")

    # 2. 类别型随机响应示例
    # 场景：用户对手机品牌投票，真实分布 A=50%, B=30%, C=20%
    categories = ["Apple", "Samsung", "Xiaomi"]
    true_brands = (
        ["Apple"] * 5000 + ["Samsung"] * 3000 + ["Xiaomi"] * 2000
    )
    api.rng.shuffle(true_brands)

    # 每个用户本地扰动自己的投票
    reported_brands = api.perturb_categorical_batch(true_brands, categories, epsilon=1.0)

    # 服务器估计真实分布
    hist = api.estimate_categorical_histogram(reported_brands, categories, epsilon=1.0)
    print(f"类别型随机响应")
    print(f"  真实分布: Apple=50%, Samsung=30%, Xiaomi=20%")
    print(f"  纠偏估计:")
    for c in categories:
        print(f"    {c}: {hist[c]:.2%}")


if __name__ == "__main__":
    main()
