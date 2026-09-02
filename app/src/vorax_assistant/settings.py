from dataclasses import asdict, dataclass
import json
from pathlib import Path

import httpx


def server_url(value: str) -> str:
    value = value.strip()
    if "://" not in value:
        value = "http://" + value
    try:
        url = httpx.URL(value)
        if (url.scheme not in ("http", "https") or not url.host
                or url.userinfo or url.query or url.fragment
                or any(char.isspace() for char in value)):
            raise ValueError
    except (httpx.InvalidURL, ValueError) as exc:
        raise ValueError("请输入 HTTP/HTTPS 服务器地址，例如 http://127.0.0.1:8080") from exc
    return str(url).rstrip("/")


@dataclass
class Settings:
    server: str = "http://127.0.0.1:8080"
    show_guidance: bool = True
    decision_backend: str = "cloud"
    local_model: str = ""
    test_mode: bool = False

    @classmethod
    def load(cls, path: Path) -> "Settings":
        if not path.exists():
            return cls()
        data = json.loads(path.read_text(encoding="utf-8"))
        settings = cls(**data)
        settings.server = server_url(settings.server)
        if not isinstance(settings.show_guidance, bool):
            raise ValueError("设置文件中的 show_guidance 必须为布尔值")
        if settings.decision_backend not in ("cloud", "local"):
            raise ValueError("设置文件中的 decision_backend 必须为 cloud 或 local")
        if not isinstance(settings.local_model, str):
            raise ValueError("设置文件中的 local_model 必须为字符串")
        if not isinstance(settings.test_mode, bool):
            raise ValueError("设置文件中的 test_mode 必须为布尔值")
        return settings

    def save(self, path: Path):
        path.parent.mkdir(parents=True, exist_ok=True)
        temporary = path.with_suffix(".tmp")
        temporary.write_text(json.dumps(asdict(self), ensure_ascii=False, indent=2), encoding="utf-8")
        temporary.replace(path)
