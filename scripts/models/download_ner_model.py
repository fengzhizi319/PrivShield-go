#!/usr/bin/env python3
"""下载 Small-NER ONNX 模型与词表文件。

支持通过 ModelScope 社区进行本地下载，以及使用对应的 urllib 镜像下载。
"""

import os
import shutil
import sys
import urllib.request


def download_via_modelscope(model_id: str, local_dir: str) -> bool:
    """使用 ModelScope SDK 下载 CMeEE 命名实体识别模型。"""
    try:
        from modelscope import snapshot_download

        print(f"[*] 正在通过 ModelScope 社区高速下载模型 {model_id} ...")
        # 下载整个仓库文件到本地指定文件夹中（含 PyTorch 权重、vocab.txt 等）
        snapshot_download(model_id, local_dir=local_dir)
        print("[+] ModelScope 下载完成！")
        return True
    except ImportError:
        print("[-] 未检测到 modelscope 库，跳过 ModelScope 本地下载通道。")
        return False
    except Exception as e:
        print(f"[-] ModelScope 下载遇到异常: {e}")
        return False


def download_file(url: str, target_path: str) -> bool:
    """使用 urllib 下载指定 URL 文件并保存至本地。"""
    print(f"[*] 正在从镜像站下载 {url} -> {target_path} ...")

    headers = {"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"}
    hf_token = os.environ.get("HF_TOKEN")
    if hf_token:
        headers["Authorization"] = f"Bearer {hf_token}"
        print("[*] 已携带本地 HF_TOKEN 进行授权访问。")

    req = urllib.request.Request(url, headers=headers)

    try:
        # timeout=60：限制连接建立与单次 socket 读阻塞的最长秒数，
        # 防止镜像站无响应时下载进程无限挂起（分块下载期间每次 read 均受约束）
        with urllib.request.urlopen(req, timeout=60) as response:
            with open(target_path, "wb") as out_file:
                # 采用 1MB 分块读取，防止大文件下载时内存溢出
                chunk_size = 1024 * 1024
                while True:
                    chunk = response.read(chunk_size)
                    if not chunk:
                        break
                    out_file.write(chunk)
        print(f"[+] 下载完成并保存: {target_path}")
        return True
    except Exception as e:
        print(f"[-] 下载失败: {e}")
        return False


import argparse


def main():
    parser = argparse.ArgumentParser(description="下载 Small-NER ONNX 模型与词表文件 / Small-NER Downloader")
    parser.add_argument("--model-id", default="iic/nlp_raner_named-entity-recognition_chinese-base-cmeee", help="ModelScope 模型 ID")
    parser.add_argument("--output-dir", "-o", help="本地保存路径 (默认: .models)")
    args = parser.parse_args()

    model_id = args.model_id

    current_dir = os.path.dirname(os.path.abspath(__file__))
    engine_models_dir = os.path.join(project_root, "services", "privacy-engine", ".models")
    default_dir = engine_models_dir if os.path.isdir(engine_models_dir) else os.path.join(project_root, ".models")
    models_dir = args.output_dir if args.output_dir else default_dir
    os.makedirs(models_dir, exist_ok=True)

    # 优先尝试从 ModelScope 进行下载
    raner_cmeee_dir = os.path.join(models_dir, "raner_cmeee")
    if download_via_modelscope(model_id, raner_cmeee_dir):
        # 复制其中的 vocab.txt 供本地通用 Tokenizer 使用
        vocab_src = os.path.join(raner_cmeee_dir, "vocab.txt")
        vocab_dst = os.path.join(models_dir, "vocab.txt")
        if os.path.exists(vocab_src):
            shutil.copy(vocab_src, vocab_dst)
            print(f"[+] 已同步 ModelScope 词表文件到: {vocab_dst}")
        print("[+] ModelScope 模型及词表配置成功！")
        sys.exit(0)

    # 如果 ModelScope 下载失败或未安装，回退至从 Hugging Face 镜像站下载 ONNX
    print("[*] 正在切换至 Hugging Face 镜像源下载 ONNX 轻量模型...")
    vocab_url = "https://hf-mirror.com/datasets/ZTYNKFX/cmeee-bucket/resolve/main/vocab.txt"
    model_url = "https://hf-mirror.com/datasets/ZTYNKFX/cmeee-bucket/resolve/main/raner_cmeee.onnx"

    vocab_target = os.path.join(models_dir, "vocab.txt")
    model_target = os.path.join(models_dir, "raner_cmeee.onnx")

    # 1. 下载词表文件
    if not download_file(vocab_url, vocab_target):
        print("[-] 词表下载失败，退出。")
        sys.exit(1)

    # 2. 下载 ONNX 模型文件
    if not download_file(model_url, model_target):
        print("[-] ONNX 模型下载失败，退出。")
        sys.exit(1)

    print("[+] ONNX 模型及词表下载配置完毕！")
    sys.exit(0)


if __name__ == "__main__":
    main()
