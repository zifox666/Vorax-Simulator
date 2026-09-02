from __future__ import annotations

from collections.abc import Sequence
from typing import Any

import gymnasium as gym
import httpx
import numpy as np
from gymnasium.vector import AutoresetMode, VectorEnv
from gymnasium.vector.utils import batch_space


class VoraxAPIError(RuntimeError):
    def __init__(self, status: int, code: str, message: str):
        super().__init__(f"{code}: {message}")
        self.status, self.code, self.message = status, code, message


class _Client:
    def __init__(self, base_url: str, api_key: str, *, transport: httpx.BaseTransport | None = None):
        self.http = httpx.Client(
            base_url=base_url.rstrip("/"),
            headers={"Authorization": f"Bearer {api_key}"},
            timeout=30,
            transport=transport,
        )

    def request(self, method: str, path: str, payload: dict[str, Any] | None = None) -> dict[str, Any]:
        response = self.http.request(method, path, json=payload)
        data = response.json()
        if response.is_error:
            raise VoraxAPIError(response.status_code, data.get("code", "HTTP_ERROR"), data.get("message", response.text))
        return data

    def close(self) -> None:
        self.http.close()


def _box(high: int, shape: tuple[int, ...], dtype: np.dtype[Any]) -> gym.spaces.Box:
    return gym.spaces.Box(low=0, high=high, shape=shape, dtype=dtype)


def _spaces(spec: dict[str, Any]) -> tuple[gym.spaces.Dict, gym.spaces.Discrete]:
    tensor, actions = spec["tensor"], len(spec["actions"])
    tools, monsters, cards = int(tensor["toolCount"]), len(spec["monsterIds"]), len(spec["cardIds"])
    i32, i64 = np.dtype(np.int32), np.dtype(np.int64)
    observation = gym.spaces.Dict(
        {
            "phase": _box(3, (1,), i32),
            "progress": _box(13, (2,), i32),
            "score": _box(np.iinfo(np.int64).max, (1,), i64),
            "slot_monsters": _box(monsters, (6,), i32),
            "slot_families": _box(4, (6,), i32),
            "slot_rarities": _box(4, (6,), i32),
            "slot_activities": _box(np.iinfo(np.int64).max, (6,), i64),
            "slot_quantities": _box(np.iinfo(np.int64).max, (6,), i64),
            "tool_counts": _box(3, (tools,), i32),
            "offer": _box(4, (1,), i32),
            "offer_reward_threshold": _box(np.iinfo(np.int64).max, (1,), i64),
            "candidate_cards": _box(cards, (5,), i32),
            "candidate_playable": _box(1, (5,), i32),
            "refreshes": _box(3, (2,), i32),
            "reward_jars": _box(4, (6,), i32),
            "drop_bonus_percent": _box(100, (1,), i32),
            "tool_claim_statuses": _box(2, (2,), i32),
            "next_reward_threshold": _box(np.iinfo(np.int64).max, (1,), i64),
            "action_mask": _box(1, (actions,), np.dtype(np.int8)),
        }
    )
    return observation, gym.spaces.Discrete(actions)


def _array(values: Any, dtype: np.dtype[Any]) -> np.ndarray:
    return np.asarray(values, dtype=dtype)


def _observation(transition: dict[str, Any]) -> dict[str, np.ndarray]:
    tensor = transition["tensorObservation"]
    i32, i64 = np.int32, np.int64
    return {
        "phase": _array([tensor["phase"]], i32),
        "progress": _array(tensor["progress"], i32),
        "score": _array([tensor["score"]], i64),
        "slot_monsters": _array(tensor["slotMonsters"], i32),
        "slot_families": _array(tensor["slotFamilies"], i32),
        "slot_rarities": _array(tensor["slotRarities"], i32),
        "slot_activities": _array(tensor["slotActivities"], i64),
        "slot_quantities": _array(tensor["slotQuantities"], i64),
        "tool_counts": _array(tensor["toolCounts"], i32),
        "offer": _array(tensor["offer"], i32),
        "offer_reward_threshold": _array([tensor["offerRewardThreshold"]], i64),
        "candidate_cards": _array(tensor["candidateCards"], i32),
        "candidate_playable": _array(tensor["candidatePlayable"], i32),
        "refreshes": _array(tensor["refreshes"], i32),
        "reward_jars": _array(tensor["rewardJars"], i32),
        "drop_bonus_percent": _array([tensor["dropBonusPercent"]], i32),
        "tool_claim_statuses": _array(tensor["toolClaimStatuses"], i32),
        "next_reward_threshold": _array([tensor["nextRewardThreshold"]], i64),
        "action_mask": _array(transition["actionMask"], np.int8),
    }


def _info(transition: dict[str, Any]) -> dict[str, Any]:
    return {
        "semantic_observation": transition["observation"],
        "legal_actions": transition["legalActions"],
        "action_mask": _array(transition["actionMask"], np.int8),
        "score": int(transition["info"]["score"]),
        "versions": transition["info"],
    }


class VoraxEnv(gym.Env[dict[str, np.ndarray], int]):
    metadata = {"render_modes": []}

    def __init__(self, base_url: str, api_key: str, *, pet_refreshes: int = 0, transport: httpx.BaseTransport | None = None):
        self.client = _Client(base_url, api_key, transport=transport)
        self.specification = self.client.request("GET", "/api/v1/training/spec")
        self.observation_space, self.action_space = _spaces(self.specification)
        self.pet_refreshes = pet_refreshes
        self.episode_token: str | None = None
        self._terminated = False
        self._action_mask = np.zeros(self.action_space.n, dtype=np.bool_)

    def reset(self, *, seed: int | None = None, options: dict[str, Any] | None = None):
        super().reset(seed=seed)
        options = options or {}
        payload = {"petRefreshes": int(options.get("pet_refreshes", self.pet_refreshes))}
        if seed is not None:
            payload["seed"] = str(seed)
        transition = self.client.request("POST", "/api/v1/training/reset", payload)
        self.episode_token, self._terminated = transition["episodeToken"], False
        self._action_mask = _array(transition["actionMask"], np.bool_)
        return _observation(transition), _info(transition)

    def step(self, action: int):
        if self.episode_token is None or self._terminated:
            raise RuntimeError("reset() must be called before step() or after termination")
        transition = self.client.request("POST", "/api/v1/training/step", {"episodeToken": self.episode_token, "actionIndex": int(action)})
        self.episode_token = transition["episodeToken"]
        self._terminated = bool(transition["terminated"])
        self._action_mask = _array(transition["actionMask"], np.bool_)
        return _observation(transition), float(transition["reward"]), self._terminated, bool(transition["truncated"]), _info(transition)

    def action_masks(self) -> np.ndarray:
        """Return the valid-action mask expected by sb3-contrib MaskablePPO."""
        return self._action_mask.copy()

    def close(self) -> None:
        self.client.close()


class VoraxVectorEnv(VectorEnv):
    metadata = {"autoreset_mode": AutoresetMode.DISABLED, "render_modes": []}

    def __init__(self, base_url: str, api_key: str, num_envs: int, *, pet_refreshes: int = 0, transport: httpx.BaseTransport | None = None):
        if num_envs < 1 or num_envs > 256:
            raise ValueError("num_envs must be between 1 and 256")
        self.client = _Client(base_url, api_key, transport=transport)
        self.specification = self.client.request("GET", "/api/v1/training/spec")
        self.num_envs = num_envs
        self.single_observation_space, self.single_action_space = _spaces(self.specification)
        self.observation_space = batch_space(self.single_observation_space, num_envs)
        self.action_space = batch_space(self.single_action_space, num_envs)
        self.pet_refreshes = pet_refreshes
        self.episode_tokens: list[str | None] = [None] * num_envs
        self.terminations = np.zeros(num_envs, dtype=np.bool_)
        self._observations: list[dict[str, np.ndarray] | None] = [None] * num_envs
        self._infos: list[dict[str, Any] | None] = [None] * num_envs
        self.closed = False

    def reset(self, *, seed: int | Sequence[int | None] | None = None, options: dict[str, Any] | None = None):
        options = options or {}
        mask = np.asarray(options.get("reset_mask", np.ones(self.num_envs, dtype=np.bool_)), dtype=np.bool_)
        if mask.shape != (self.num_envs,) or not mask.any():
            raise ValueError("reset_mask must select at least one environment")
        seeds = list(seed) if isinstance(seed, Sequence) and not isinstance(seed, (str, bytes)) else [None if seed is None else int(seed) + i for i in range(self.num_envs)]
        pets = options.get("pet_refreshes", self.pet_refreshes)
        pets = list(pets) if isinstance(pets, Sequence) and not isinstance(pets, (str, bytes)) else [pets] * self.num_envs
        indexes, items = [], []
        for i in range(self.num_envs):
            if mask[i]:
                indexes.append(i)
                item: dict[str, Any] = {"petRefreshes": int(pets[i])}
                if seeds[i] is not None:
                    item["seed"] = str(seeds[i])
                items.append(item)
        results = self.client.request("POST", "/api/v1/training/batch/reset", {"items": items})["results"]
        for index, result in zip(indexes, results, strict=True):
            transition = _unwrap(result)
            self.episode_tokens[index] = transition["episodeToken"]
            self.terminations[index] = False
            self._observations[index], self._infos[index] = _observation(transition), _info(transition)
        if any(value is None for value in self._observations):
            raise RuntimeError("all environments must be reset before use")
        return _stack(self._observations), _vector_info(self._infos)

    def step(self, actions: np.ndarray):
        actions = np.asarray(actions)
        if actions.shape != (self.num_envs,):
            raise ValueError(f"actions must have shape ({self.num_envs},)")
        if any(token is None for token in self.episode_tokens) or self.terminations.any():
            raise RuntimeError("reset terminated environments with options={'reset_mask': mask} before step()")
        items = [{"episodeToken": token, "actionIndex": int(action)} for token, action in zip(self.episode_tokens, actions, strict=True)]
        results = self.client.request("POST", "/api/v1/training/batch/step", {"items": items})["results"]
        rewards, truncations = np.zeros(self.num_envs), np.zeros(self.num_envs, dtype=np.bool_)
        for i, result in enumerate(results):
            if "error" in result:
                error = result["error"]
                mask = self._observations[i]["action_mask"]
                action = int(actions[i])
                locally_allowed = 0 <= action < len(mask) and bool(mask[action])
                message = error.get("message", "batch item failed")
                raise VoraxAPIError(
                    400,
                    error.get("code", "ITEM_ERROR"),
                    f"{message} (batch env={i}, actionIndex={action}, clientMaskAllowed={locally_allowed})",
                )
            transition = _unwrap(result)
            self.episode_tokens[i] = transition["episodeToken"]
            self.terminations[i], truncations[i] = bool(transition["terminated"]), bool(transition["truncated"])
            rewards[i] = float(transition["reward"])
            self._observations[i], self._infos[i] = _observation(transition), _info(transition)
        return _stack(self._observations), rewards, self.terminations.copy(), truncations, _vector_info(self._infos)

    def close_extras(self, **kwargs: Any) -> None:
        self.client.close()


def _unwrap(result: dict[str, Any]) -> dict[str, Any]:
    if "error" in result:
        error = result["error"]
        raise VoraxAPIError(400, error.get("code", "ITEM_ERROR"), error.get("message", "batch item failed"))
    return result["transition"]


def _stack(observations: Sequence[dict[str, np.ndarray] | None]) -> dict[str, np.ndarray]:
    concrete = [value for value in observations if value is not None]
    return {key: np.stack([value[key] for value in concrete]) for key in concrete[0]}


def _vector_info(infos: Sequence[dict[str, Any] | None]) -> dict[str, np.ndarray]:
    concrete = [value for value in infos if value is not None]
    return {key: np.asarray([value[key] for value in concrete], dtype=object) for key in concrete[0]}
