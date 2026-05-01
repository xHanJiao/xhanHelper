import os
import re

import yaml


def _load_yaml(path: str) -> dict:
    with open(path) as f:
        raw = f.read()
    raw = _expand_env(raw)
    return yaml.safe_load(raw) or {}


def _expand_env(text: str) -> str:
    def replacer(m):
        return os.environ.get(m.group(1), "")
    return re.sub(r"\$\{(\w+)\}", replacer, text)


def _merge(base: dict, override: dict) -> dict:
    for k, v in override.items():
        if isinstance(v, dict) and isinstance(base.get(k), dict):
            base[k] = _merge(base[k], v)
        elif v not in (None, "", 0):
            base[k] = v
    return base


class Settings:
    def __init__(self):
        env = os.environ.get("APP_ENV", "dev")

        cfg = _load_yaml("conf/config.yaml")

        env_path = f"conf/config.{env}.yaml"
        if os.path.exists(env_path):
            cfg = _merge(cfg, _load_yaml(env_path))

        self.app_env = env
        self.server_port = cfg.get("server", {}).get("port", 8080)

        db = cfg.get("db", {})
        self.db_driver = db.get("driver", "sqlite")
        self.db_host = db.get("host", "127.0.0.1")
        self.db_port = db.get("port", 3306)
        self.db_name = db.get("database", "xhan_helper")
        self.db_user = db.get("user", "root")
        self.db_password = db.get("password", "")

        llm = cfg.get("llm", {})
        self.llm_provider = llm.get("provider", "doubao")
        self.llm_api_key = llm.get("api_key", "")
        self.llm_model = llm.get("model", "doubao-seed-1-8-251228")
        self.llm_base_url = llm.get("base_url", "https://ark.cn-beijing.volces.com/api/v3")

        self.registration_secret = cfg.get("registration_secret", "dev-secret")

    @property
    def db_url(self) -> str:
        if self.db_driver == "sqlite":
            return f"sqlite+aiosqlite:///./{self.db_name}"
        return (
            f"mysql+aiomysql://{self.db_user}:{self.db_password}"
            f"@{self.db_host}:{self.db_port}/{self.db_name}"
            f"?charset=utf8mb4"
        )


settings = Settings()
