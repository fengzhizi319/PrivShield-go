#!/usr/bin/env python3
"""本地多模态大模型一键下载工具。

支持通过 ModelScope (首选) 或 Hugging Face 镜像站高速下载 Qwen2-VL-2B-Instruct 模型权重。
"""

import os
import sys


def download_via_modelscope(model_id: str, local_dir: str) -> bool:
    """尝试通过 ModelScope 下载模型。"""
    try:
        from modelscope import snapshot_download
        print(f"[*] 正在通过 ModelScope 下载模型 {model_id} ...")
        snapshot_download(model_id, local_dir=local_dir)
        print("[+] ModelScope 下载完成！")
        return True
    except ImportError:
        print("[-] 未检测到 modelscope 库。可通过 'pip install modelscope' 安装以获取最快下载速度。")
        return False
    except Exception as e:
        print(f"[-] ModelScope 下载遇到错误: {e}")
        return False


def download_via_huggingface(model_id: str, local_dir: str) -> bool:
    """尝试通过 Hugging Face 镜像源下载模型。"""
    try:
        # 如果未设置，默认配置国内高速镜像源
        if "HF_ENDPOINT" not in os.environ:
            os.environ["HF_ENDPOINT"] = "https://hf-mirror.com"
        from huggingface_hub import snapshot_download
        print(f"[*] 正在通过 Hugging Face 镜像下载模型 {model_id} (HF_ENDPOINT={os.environ['HF_ENDPOINT']}) ...")
        snapshot_download(repo_id=model_id, local_dir=local_dir, ignore_patterns=["*.msgpack", "*.h5", "*.ot"])
        print("[+] Hugging Face 下载完成！")
        return True
    except ImportError:
        print("[-] 未检测到 huggingface_hub 库。可通过 'pip install huggingface_hub' 安装。")
        return False
    except Exception as e:
        print(f"[-] Hugging Face 下载遇到错误: {e}")
        return False


import argparse


def main():
    parser = argparse.ArgumentParser(description="本地大语言模型下载工具 / LLM Downloader")
    parser.add_argument("--model-id", default="Qwen/Qwen2.5-0.5B-Instruct", help="模型 ID (默认: Qwen/Qwen2.5-0.5B-Instruct)")
    parser.add_argument("--output-dir", "-o", help="本地保存路径 (默认: .models/<model_name>)")
    args = parser.parse_args()

    model_id = args.model_id
    
    current_dir = os.path.dirname(os.path.abspath(__file__))
    project_root = os.path.dirname(os.path.dirname(current_dir))
    
    if args.output_dir:
        local_dir = args.output_dir
    else:
        model_name = model_id.split("/")[-1]
        engine_models_dir = os.path.join(project_root, "services", "privacy-engine", ".models")
        if os.path.isdir(engine_models_dir):
            local_dir = os.path.join(engine_models_dir, model_name)
        else:
            local_dir = os.path.join(project_root, ".models", model_name)
    
    print(f"[*] 目标保存路径: {local_dir}")
    os.makedirs(local_dir, exist_ok=True)
    
    # 优先使用 ModelScope
    success = download_via_modelscope(model_id, local_dir)
    if not success:
        print("[*] 正在切换至 Hugging Face 镜像重试...")
        success = download_via_huggingface(model_id, local_dir)
        
    if success:
        print(f"[+] 大模型下载成功！已保存在: {local_dir}")
        sys.exit(0)
    else:
        print("[-] 错误：无法下载模型。请确保安装了 modelscope 或 huggingface_hub:")
        print("    pip install modelscope huggingface_hub")
        sys.exit(1)


if __name__ == "__main__":
    main()
