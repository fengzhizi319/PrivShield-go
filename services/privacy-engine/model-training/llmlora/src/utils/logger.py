# -*- coding: utf-8 -*-
"""
llmlora 日志服务模块
"""
import logging
import sys


def setup_logger(name: str = "llmlora", level: int = logging.INFO) -> logging.Logger:
    """初始化标准的统一 Logger 控制台与日志输出"""
    logger = logging.getLogger(name)
    logger.setLevel(level)
    
    if not logger.handlers:
        handler = logging.StreamHandler(sys.stdout)
        formatter = logging.Formatter(
            "[%(asctime)s] [%(levelname)s] [%(name)s] %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S"
        )
        handler.setFormatter(formatter)
        logger.addHandler(handler)
        
    return logger
