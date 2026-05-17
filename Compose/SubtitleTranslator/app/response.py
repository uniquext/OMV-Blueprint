"""
API 统一响应包装器

所有 API 端点（除 SSE 外）的响应统一为 {"code": int, "message": str, "data": object|null} 结构。
"""

from typing import Any, Optional


def api_success(data: Any = None, message: str = "ok") -> dict:
    """
    构造成功响应信封

    Args:
        data: 响应数据，可以是任意类型，None 时序列化为 null
        message: 简要描述，默认为 "ok"

    Returns:
        统一格式的响应字典
    """
    return {
        "code": 200,
        "message": message,
        "data": data,
    }


def api_error(code: int, message: str, data: Any = None) -> dict:
    """
    构造错误响应信封

    Args:
        code: HTTP 状态码
        message: 错误描述
        data: 附加错误详情，默认为 None

    Returns:
        统一格式的响应字典
    """
    return {
        "code": code,
        "message": message,
        "data": data,
    }
