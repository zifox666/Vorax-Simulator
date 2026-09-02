import asyncio
from datetime import datetime
import json
from pathlib import Path

from .client import Client, ServiceError
from .local_model import LocalDecisionModel
from .ocr import Frame, Reader
from .parser import Parser
from .session import Session


class Controller:
    def __init__(self, url: str, pet: int, models: Path, data: Path, rollouts: int,
                 decision_models: Path | None = None, decision_backend: str = "cloud", local_model: str = "",
                 test_mode: bool = False):
        self.client = Client(url, rollouts)
        self.models, self.data = models, data
        self.decision_models = decision_models or models.parent / "models"
        self.decision_backend, self.local_model_name = decision_backend, local_model
        self.test_mode = test_mode
        self.local_model = None
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
        self.local_model = None
        self.last_observation = None
        self.server_changed = True
        # Keep the last shown tool recommendation for the next acquisition.

    async def initialize(self):
        self.catalog = await self.client.catalog()
        if self.decision_backend == "local" and self.local_model is None:
            specification = await self.client.model_spec()
            self.local_model = await asyncio.to_thread(
                LocalDecisionModel, self.decision_models, self.local_model_name, specification, self.test_mode
            )
        if self.reader is None:
            self.reader = await asyncio.to_thread(Reader, self.models)

    async def change_decision(self, backend: str, model_name: str = "", test_mode: bool = False):
        if backend not in ("cloud", "local"):
            raise ValueError("决策方式必须为 cloud 或 local")
        changed = (backend != self.decision_backend or model_name != self.local_model_name
                   or test_mode != self.test_mode)
        self.decision_backend, self.local_model_name = backend, model_name
        self.test_mode = test_mode
        if changed:
            self.local_model = None
            self.last_observation = None
            self.server_changed = True

    async def accept(self, frame: Frame, _version_retry: bool = False) -> dict:
        if self.catalog is None:
            self.catalog = await self.client.catalog()
        self.data.mkdir(parents=True, exist_ok=True)
        (self.data / "last-ocr.json").write_text(json.dumps(frame.to_dict(), ensure_ascii=False, indent=2), encoding="utf-8")
        snapshot = Parser(self.catalog).parse(frame)
        candidate, visible = self.session.prepare(snapshot, self.catalog, self.test_mode)
        # Store observed progress even if the network fails, but never create a
        # tool acquisition from a response that was not shown to the player.
        candidate.save(self.path)
        self.session = candidate
        if candidate.suggestion is not None and not self.server_changed:
            return {"action": candidate.suggestion, "observation": self.last_observation, "cached": True}
        try:
            if self.decision_backend == "local":
                if self.local_model is None:
                    await self.initialize()
                encoded = await self.client.model_input(visible)
                action = await asyncio.to_thread(self.local_model.predict, encoded)
                response = {"action": action, "observation": encoded.get("observation"), "backend": "local"}
            else:
                response = await self.client.suggest(visible)
                response["backend"] = "cloud"
        except ServiceError as exc:
            if self.test_mode and not _version_retry and "版本不匹配" in str(exc):
                self.catalog = await self.client.catalog()
                self.local_model = None
                self.last_observation = None
                self.server_changed = True
                return await self.accept(frame, _version_retry=True)
            raise
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

    @property
    def model_warning(self) -> str:
        return getattr(self.local_model, "warning", "") if self.local_model is not None else ""
