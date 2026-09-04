#!/usr/bin/env bash
# ==============================================================================
# vLLM Server Launcher for PrivShield
# 项目根目录 vLLM 服务启动脚本
# ==============================================================================
set -e

# 获取项目根目录
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

# 自动读取 .env 环境变量
if [ -f "$ROOT_DIR/.env" ]; then
    echo "⚙️  加载环境配置文件: $ROOT_DIR/.env"
    set -a
    source "$ROOT_DIR/.env"
    set +a
fi

# 参数默认值配置
HOST="${PRIVACY_LLM_API_HOST:-127.0.0.1}"
DEFAULT_MODEL_PATH="services/privacy-engine/.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother"
if [ ! -d "$ROOT_DIR/$DEFAULT_MODEL_PATH" ] && [ -d "$ROOT_DIR/.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother" ]; then
    DEFAULT_MODEL_PATH=".models/Qwen3.5-0.8B-Privacy-Classifier-Smoother"
fi
MODEL_PATH="${PRIVACY_LLM_MODEL_PATH:-$DEFAULT_MODEL_PATH}"
SERVED_NAME="${PRIVACY_LLM_MODEL_NAME:-Qwen3.5-0.8B-Privacy-Classifier-Smoother}"
GPU_UTIL="${PRIVACY_VLLM_GPU_MEMORY_UTILIZATION:-0.90}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  --host HOST         监听地址 (默认: 127.0.0.1 或 PRIVACY_LLM_API_HOST)"
            echo "  --port PORT         监听端口 (默认: 8000 或 PRIVACY_LLM_API_PORT)"
            echo "  --model PATH        模型权重路径或 HuggingFace ID"
            echo "  --gpu-util UTIL     GPU 显存占用比例 (默认: 0.90)"
            echo "  -h, --help          显示帮助信息并退出"
            exit 0
            ;;
        --host)
            HOST="$2"
            shift 2
            ;;
        --port)
            PORT="$2"
            shift 2
            ;;
        --model)
            MODEL_PATH="$2"
            shift 2
            ;;
        --gpu-util)
            GPU_UTIL="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

# 若模型目录不存在，退而使用备选模型名
if [ ! -d "$MODEL_PATH" ]; then
    echo "⚠️  未在 $MODEL_PATH 找到本地权重文件"
    if [ -n "$PRIVACY_LLM_MODEL_NAME" ] && [ "$PRIVACY_LLM_MODEL_NAME" != "Qwen3.5-0.8B-Privacy-Classifier-Smoother" ]; then
        MODEL_PATH="$PRIVACY_LLM_MODEL_NAME"
    else
        MODEL_PATH="Qwen/Qwen3.5-0.8B"
    fi
    echo "ℹ️  改用 HuggingFace 开源权重名称: $MODEL_PATH"
fi

echo "🚀 启动 vLLM OpenAI 兼容 HTTP 服务..."
echo "--------------------------------------------------------"
echo "  主机: $HOST"
echo "  端口: $PORT"
echo "  模型路径: $MODEL_PATH"
echo "  对外模型名: $SERVED_NAME"
echo "  API Endpoint: http://$HOST:$PORT/v1/chat/completions"
echo "--------------------------------------------------------"

# 检查 vllm 命令或 python 模块是否可用
if command -v vllm &> /dev/null; then
    exec vllm serve "$MODEL_PATH" \
        --host "$HOST" \
        --port "$PORT" \
        --served-model-name "$SERVED_NAME" \
        --trust-remote-code \
        --gpu-memory-utilization "$GPU_UTIL"
elif python -c "import vllm" &> /dev/null; then
    exec python -m vllm.entrypoints.openai.api_server \
        --model "$MODEL_PATH" \
        --host "$HOST" \
        --port "$PORT" \
        --served-model-name "$SERVED_NAME" \
        --trust-remote-code \
        --gpu-memory-utilization "$GPU_UTIL"
else
    echo "❌ 错误: 未检测到 vllm 依赖包。请先安装: pip install vllm"
    echo "提示: 在 CPU 环境或未安装 GPU 的轻量环境中，您仍可通过修改 .env 中的 PRIVACY_LLM_PROVIDER 使用 provider=qwen3 或 provider=openai。"
    exit 1
fi
