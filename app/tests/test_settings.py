import asyncio
import json
from pathlib import Path

import httpx
import pytest

from vorax_assistant.client import Client
from vorax_assistant.controller import Controller
from vorax_assistant.ocr import Frame
from vorax_assistant.settings import Settings, server_url


@pytest.mark.parametrize(("value", "expected"), [
    (" 127.0.0.1:8080/ ", "http://127.0.0.1:8080"),
    ("https://example.com/ai/", "https://example.com/ai"),
    ("http://[::1]:8080", "http://[::1]:8080"),
])
def test_server_address_normalization(value, expected):
    assert server_url(value) == expected


@pytest.mark.parametrize("value", ["", "http://", "ftp://example.com", "http://example.com:abc",
                                     "http://bad host", "https://example.com/#token", "http://user:secret@example.com"])
def test_invalid_server_address(value):
    with pytest.raises(ValueError, match="服务器地址"):
        server_url(value)


def test_settings_persist_without_touching_session(tmp_path):
    path = tmp_path / "settings.json"
    assert Settings.load(path) == Settings()
    settings = Settings("http://localhost:9000", False)
    settings.save(path)
    assert Settings.load(path) == settings
    assert not (tmp_path / "session.json").exists()


@pytest.mark.parametrize("next_frame", [2, 3])
def test_switch_server_preserves_recommendation_and_recomputes(tmp_path, monkeypatch, next_frame):
    fixtures = Path(__file__).parent / "fixtures"
    catalog = json.loads((fixtures / "catalog.json").read_text(encoding="utf-8"))
    frames = {n: Frame.from_dict(json.loads((fixtures / f"{n}.json").read_text(encoding="utf-8"))) for n in (2, 3)}

    async def run():
        calls = []

        def responder(request):
            if request.method == "GET":
                return httpx.Response(200, json=catalog)
            calls.append((request.url.host, json.loads(request.content)))
            card = "claw" if next_frame == 2 or len(calls) == 1 else "fiend_fluid"
            return httpx.Response(200, json={"action": {"type": "choose", "cardId": card, "targetSlots": []}, "observation": {}})

        c = Controller("http://old.local", 0, tmp_path, tmp_path, 1)
        await c.client.close()
        old_http = httpx.AsyncClient(transport=httpx.MockTransport(responder), base_url="http://old.local")
        c.client.http = old_http
        try:
            await c.accept(frames[2])
            monkeypatch.setattr("vorax_assistant.controller.Client", lambda url, rollouts: Client(url, rollouts))
            await c.change_server("http://next.local")
            assert old_http.is_closed and c.client.rollouts == 1
            assert c.session.suggestion["cardId"] == "claw" and not c.session.tools
            await c.client.close()
            c.client.http = httpx.AsyncClient(transport=httpx.MockTransport(responder), base_url="http://next.local")
            await c.accept(frames[next_frame])
            assert [host for host, _ in calls] == ["old.local", "next.local"]
            assert c.session.tools == (["claw"] if next_frame == 3 else [])
            assert not c.server_changed
        finally:
            await c.close()
    asyncio.run(run())
