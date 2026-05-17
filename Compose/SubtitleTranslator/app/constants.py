"""
全局常量定义

集中管理跨模块共享的常量，避免魔法字符串分散在各处。
"""

# 终态集合：Job/Task 进入这些状态后即视为生命周期结束
TERMINAL_STATES = {"done", "failed", "skipped"}
