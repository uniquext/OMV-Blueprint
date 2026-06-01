"""
Pydantic 配置校验模型

用于 PUT /api/config 的自动校验，定义了完整的配置结构。
extra='ignore' 自动过滤 app_port 等未定义字段。
"""
from typing import Dict, List
from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


class LlmConfig(BaseModel):
    """LLM 配置"""
    model_config = ConfigDict(extra="ignore", strict=True)

    api_url: str
    api_key: str
    model: str
    model_type: str
    temperature: float = Field(ge=0, le=2)
    timeout: int = Field(ge=1, le=600)
    batch_size: int = Field(ge=1, le=100)
    context_size: int = Field(ge=0, le=20)
    rpm_limit: int = Field(ge=0, le=100000)
    tpm_limit: int = Field(ge=0, le=10000000)
    max_retries: int = Field(ge=0, le=10)

    @field_validator("api_url", "api_key", "model")
    @classmethod
    def _must_not_be_empty(cls, v: str) -> str:
        if not v or not v.strip():
            raise ValueError("must not be empty")
        return v

    @field_validator("model_type")
    @classmethod
    def _valid_model_type(cls, v: str) -> str:
        if v not in ("chat", "mt"):
            raise ValueError("must be 'chat' or 'mt'")
        return v


class StrategyConfig(BaseModel):
    """漏斗策略矩阵配置"""
    model_config = ConfigDict(extra="ignore", strict=True)

    location_priority: str
    language_priority: str

    @field_validator("location_priority")
    @classmethod
    def _valid_location(cls, v: str) -> str:
        if v not in ("internal", "external"):
            raise ValueError("must be 'internal' or 'external'")
        return v

    @field_validator("language_priority")
    @classmethod
    def _valid_language(cls, v: str) -> str:
        if v not in ("chinese", "absolute"):
            raise ValueError("must be 'chinese' or 'absolute'")
        return v


class PipelineConfig(BaseModel):
    """管道配置"""
    model_config = ConfigDict(extra="ignore", strict=True)

    debounce_seconds: int = Field(ge=0, le=600)
    debounce_poll_interval: int = Field(ge=1, le=60)
    funnel_workers: int = Field(ge=1, le=20)
    scan_interval: int = Field(ge=0, le=86400)
    scan_dir: str
    ignore_list: str = ""

    @field_validator("scan_dir")
    @classmethod
    def _must_not_be_empty(cls, v: str) -> str:
        if not v or not v.strip():
            raise ValueError("must not be empty")
        return v

    @model_validator(mode='after')
    def _validate_ignore_list(self) -> 'PipelineConfig':
        if not getattr(self, 'ignore_list', None):
            return self
            
        ignores = [i.strip() for i in self.ignore_list.split(",") if i.strip()]
        if not ignores:
            return self
            
        import os
        # Ensure scan_dir is valid before proceeding
        if not getattr(self, 'scan_dir', None) or not os.path.isdir(self.scan_dir):
            return self
            
        scan_dir_real = os.path.realpath(self.scan_dir)
        
        for ign in ignores:
            if not ign.startswith('/'):
                raise ValueError(f"ignore_list items must be absolute paths starting with '/': {ign}")
                
            ign_real = os.path.realpath(ign)
            if ign_real != scan_dir_real and not ign_real.startswith(scan_dir_real + os.sep):
                raise ValueError(f"ignore_list item '{ign}' is outside the scan directory '{self.scan_dir}'")
                
            if not os.path.exists(ign_real):
                raise ValueError(f"ignore_list item '{ign}' does not exist")
                
            if not os.path.isdir(ign_real):
                raise ValueError(f"ignore_list item '{ign}' is not a directory")
                
        return self


class MediaConfig(BaseModel):
    """媒体配置"""
    model_config = ConfigDict(extra="ignore", strict=True)

    extensions: List[str]
    lang_map_override: Dict[str, str] = {}

    @field_validator("extensions")
    @classmethod
    def _extensions_not_empty(cls, v: List[str]) -> List[str]:
        if not v:
            raise ValueError("must not be empty")
        return v

    @field_validator("extensions")
    @classmethod
    def _extensions_dot_prefix(cls, v: List[str]) -> List[str]:
        for ext in v:
            if not ext.startswith("."):
                raise ValueError(f"extension '{ext}' must start with '.'")
        return v

    @field_validator("lang_map_override")
    @classmethod
    def _lang_map_no_empty_kv(cls, v: Dict[str, str]) -> Dict[str, str]:
        for k, val in v.items():
            if not k or not k.strip():
                raise ValueError("lang_map_override key must not be empty")
            if not val or not val.strip():
                raise ValueError(f"lang_map_override value for key '{k}' must not be empty")
        return v


class WatchdogConfig(BaseModel):
    """文件监控配置"""
    model_config = ConfigDict(extra="ignore", strict=True)

    enabled: bool
    path: str

    @field_validator("path")
    @classmethod
    def _must_not_be_empty(cls, v: str) -> str:
        if not v or not v.strip():
            raise ValueError("must not be empty")
        return v


class ConfigPayload(BaseModel):
    """完整配置校验模型（PUT /api/config 请求体）"""
    model_config = ConfigDict(extra="ignore")

    llm: LlmConfig
    pipeline: PipelineConfig
    media: MediaConfig
    watchdog: WatchdogConfig
    strategy: StrategyConfig


def mask_api_key(key: str) -> str:
    """
    api_key 长度感知脱敏

    - 空字符串 → 空字符串
    - 长度 > 8 → 前4 + •••• + 后4
    - 长度 ≤ 8 → ••••
    """
    if not key:
        return ""
    if len(key) > 8:
        return f"{key[:4]}••••{key[-4:]}"
    return "••••"
