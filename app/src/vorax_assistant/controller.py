import asyncio
from datetime import datetime
import json
from pathlib import Path

from .client import Client
from .ocr import Frame, Reader
from .parser import Parser
from .session import Session


class Controller:
    def __init__(self, url: str, pet: int, models: Path, data: Path, rollouts: int):
        self.client = Client(url, rollouts)
        self.models, self.data = models, data
        self.path = data / "session.json"
        self.session = Session.load(self.path) if self.path.exists() else Session(pet)
        self.reader = None
        self.catalog = None
        self.last_observation = None
        self.server_changed = False

    async def change_server(self, url: str):
        replacement = Client(url, self.client.rollouts)
        await self.client.close()
        self.client = replacement
        self.catalog = None
        self.last_observation = None
        self.server_changed = True
        # Keep the last shown tool recommendation for the next acquisition.

    async def initialize(self):
        self.catalog = await self.client.catalog()
        if self.reader is None:
            self.reader = await asyncio.to_thread(Reader, self.models)

    async def accept(self, frame: Frame) -> dict:
        if self.catalog is None:
            self.catalog = await self.client.catalog()
        self.data.mkdir(parents=True, exist_ok=True)
        (self.data / "last-ocr.json").write_text(json.dumps(frame.to_dict(), ensure_ascii=False, indent=2), encoding="utf-8")
        snapshot = Parser(self.catalog).parse(frame)
        candidate, visible = self.session.prepare(snapshot, self.catalog)
        # Store observed progress even if the network fails, but never create a
        # tool acquisition from a response that was not shown to the player.
        candidate.save(self.path)
        self.session = candidate
        if candidate.suggestion is not None and not self.server_changed:
            return {"action": candidate.suggestion, "observation": self.last_observation, "cached": True}
        response = await self.client.suggest(visible)
        candidate.suggestion = response["action"]
        candidate.save(self.path)
        self.last_observation = response["observation"]
        self.server_changed = False
        with (self.data / "history.jsonl").open("a", encoding="utf-8") as stream:
            stream.write(json.dumps({"time": datetime.now().isoformat(), "visible": visible, "action": response["action"]}, ensure_ascii=False) + "\n")
        return response

    async def new_session(self, pet: int):
        if self.session.last is not None:
            self.session.save(self.data / f"session-{datetime.now():%Y%m%d-%H%M%S-%f}.json")
        self.session = Session(pet)
        self.session.save(self.path)
        self.last_observation = None
        self.catalog = None

    async def close(self):
        await self.client.close()
