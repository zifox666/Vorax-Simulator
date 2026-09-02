from __future__ import annotations

from copy import deepcopy
import json
from pathlib import Path
from typing import Any


class LocalModelError(RuntimeError):
    pass


def available_models(directory: Path) -> list[str]:
    if not directory.is_dir():
        return []
    return sorted((path.name for path in directory.glob("*.zip") if path.is_file()), key=str.casefold)


def resolve_model(directory: Path, requested: str = "") -> Path:
    if requested:
        if Path(requested).name != requested or not requested.casefold().endswith(".zip"):
            raise LocalModelError("本地模型名称必须是 models 目录中的 .zip 文件名")
        model = directory / requested
        if not model.is_file():
            raise LocalModelError(f"未找到本地模型：{model}")
        return model
    candidates = [directory / name for name in available_models(directory)]
    if not candidates:
        raise LocalModelError(f"未在 {directory} 中找到模型，请放入模型 .zip（正式模式还需同名 .json）")
    if len(candidates) > 1:
        names = "、".join(path.name for path in candidates)
        raise LocalModelError(f"models 中有多个模型（{names}），请在设置中选择一个")
    return candidates[0]


class LocalDecisionModel:
    """Load a MaskablePPO artifact and make deterministic masked decisions."""

    def __init__(self, directory: Path, requested: str, specification: dict[str, Any], test_mode: bool = False):
        model_path = resolve_model(directory, requested)
        metadata_path = model_path.with_suffix(".json")
        metadata: dict[str, Any] = {}
        metadata_problem = ""
        if metadata_path.is_file():
            try:
                metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
                if not isinstance(metadata, dict):
                    raise ValueError("顶层必须是 JSON 对象")
            except (OSError, ValueError) as exc:
                if not test_mode:
                    raise LocalModelError(f"模型元数据无法读取：{exc}") from exc
                metadata_problem = f"元数据无法读取：{exc}"
        elif not test_mode:
            raise LocalModelError(f"缺少模型元数据：{metadata_path.name}；请和 .zip 一起放入 models")
        else:
            metadata_problem = f"缺少 {metadata_path.name}"

        mismatches = []
        for key in ("specVersion", "specHash", "rulesVersion", "contentVersion"):
            expected, actual = specification.get(key), metadata.get(key)
            if not actual or actual != expected:
                mismatches.append((key, actual, expected))
        if mismatches and not test_mode:
            key, actual, expected = mismatches[0]
            raise LocalModelError(
                f"模型 {key} 不匹配：模型为 {actual or '缺失'}，服务端为 {expected or '缺失'}；请使用对应版本重新训练"
            )

        try:
            import numpy as np
            from sb3_contrib import MaskablePPO
            # Import the package that owns the custom policy classes before
            # cloudpickle restores the saved SB3 policy configuration.
            import vorax_gym.policy  # noqa: F401
        except ImportError as exc:
            raise LocalModelError("当前客户端未安装本地模型运行组件，请使用完整构建版") from exc
        try:
            self.model = MaskablePPO.load(model_path, device="cpu", print_system_info=False)
        except Exception as exc:
            raise LocalModelError(f"本地模型加载失败：{exc}") from exc

        self.np = np
        self.path = model_path
        self.test_mode = test_mode
        self.current_actions = sorted(specification.get("actions", []), key=lambda item: int(item.get("index", 0)))
        model_actions = int(getattr(self.model.action_space, "n", 0) or 0)
        if model_actions <= 0:
            raise LocalModelError("本地模型没有可用的离散动作空间")

        model_spec = metadata.get("modelSpec")
        compatibility = "按模型契约映射"
        if not isinstance(model_spec, dict):
            if not mismatches:
                model_spec = specification
            else:
                model_spec = legacy_model_spec(metadata, specification, model_actions)
                if model_spec is not None:
                    compatibility = "迁移旧格式模型契约"
            if model_spec is None:
                if not test_mode:
                    raise LocalModelError("模型元数据缺少 modelSpec；请重新训练并导出完整模型契约")
                compatibility = "缺少模型契约，按位置尽力适配"

        projection = build_model_projection(model_spec, specification, model_actions)
        self.action_indices = projection["action_indices"]
        self.actions = projection["actions"]
        self.card_id_map = projection["card_id_map"]
        self.monster_id_map = projection["monster_id_map"]
        self.tool_indices = projection["tool_indices"]
        self.semantic_projection = model_spec is not None

        if not test_mode:
            if any(index is None for index in self.action_indices) or model_actions != len(self.current_actions):
                raise LocalModelError("模型动作空间与当前服务端规格不一致")
            self.warning = ""
        else:
            details = []
            if metadata_problem:
                details.append(metadata_problem)
            details.extend(f"{key}: {actual or '缺失'} → {expected or '缺失'}" for key, actual, expected in mismatches)
            suffix = f"（{'、'.join(details)}）" if details else ""
            self.warning = f"⚠ 测试模式：{compatibility}{suffix}；缺失内容将置零或屏蔽，结果仅供流程测试"

    def predict(self, transition: dict[str, Any]) -> dict[str, Any] | None:
        if transition.get("terminated"):
            return None
        tensor = transition.get("tensorObservation") or {}
        np = self.np

        candidate_cards = self._values(tensor.get("candidateCards"), "candidate_cards", np.int32)
        slot_monsters = self._values(tensor.get("slotMonsters"), "slot_monsters", np.int32)
        candidate_playable = self._values(tensor.get("candidatePlayable"), "candidate_playable", np.int32)
        tool_counts = self._project_tools(tensor.get("toolCounts"))
        if self.semantic_projection:
            candidate_cards = self._remap_ids(candidate_cards, self.card_id_map)
            slot_monsters = self._remap_ids(slot_monsters, self.monster_id_map)
            candidate_playable = candidate_playable.copy()
            candidate_playable[candidate_cards == 0] = 0

        current_mask = np.asarray(transition.get("actionMask") or [], dtype=np.bool_)
        mask = np.asarray([
            bool(current_mask[index]) if index is not None and index < len(current_mask) else False
            for index in self.action_indices
        ], dtype=np.bool_)
        if not mask.any():
            fallback = next((i for i, allowed in enumerate(current_mask) if allowed and i < len(self.current_actions)), None)
            if fallback is not None:
                return dict(self.current_actions[fallback]["action"])
            raise LocalModelError("当前数据没有可执行动作，测试模式也无法继续")

        i32, i64 = np.int32, np.int64
        observation = {
            "phase": self._values([tensor.get("phase", 0)], "phase", i32),
            "progress": self._values(tensor.get("progress"), "progress", i32),
            "score": self._values([tensor.get("score", 0)], "score", i64),
            "slot_monsters": slot_monsters,
            "slot_families": self._values(tensor.get("slotFamilies"), "slot_families", i32),
            "slot_rarities": self._values(tensor.get("slotRarities"), "slot_rarities", i32),
            "slot_activities": self._values(tensor.get("slotActivities"), "slot_activities", i64),
            "slot_quantities": self._values(tensor.get("slotQuantities"), "slot_quantities", i64),
            "tool_counts": tool_counts,
            "offer": self._values(tensor.get("offer"), "offer", i32),
            "offer_reward_threshold": self._values([tensor.get("offerRewardThreshold", 0)], "offer_reward_threshold", i64),
            "candidate_cards": candidate_cards,
            "candidate_playable": candidate_playable,
            "refreshes": self._values(tensor.get("refreshes"), "refreshes", i32),
            "reward_jars": self._values(tensor.get("rewardJars"), "reward_jars", i32),
            "drop_bonus_percent": self._values([tensor.get("dropBonusPercent", 0)], "drop_bonus_percent", i32),
            "tool_claim_statuses": self._values(tensor.get("toolClaimStatuses"), "tool_claim_statuses", i32),
            "next_reward_threshold": self._values([tensor.get("nextRewardThreshold", 0)], "next_reward_threshold", i64),
            "action_mask": self._values(mask.astype(np.int8), "action_mask", np.int8),
        }
        action, _ = self.model.predict(observation, action_masks=mask, deterministic=True)
        index = int(np.asarray(action).item())
        if index < 0 or index >= len(mask) or not mask[index]:
            raise LocalModelError(f"模型返回了当前不可执行的动作索引 {index}")
        mapped = self.actions.get(index)
        if mapped is None:
            raise LocalModelError(f"模型动作索引 {index} 在当前内容中不存在")
        return dict(mapped)

    def _values(self, values: Any, key: str, dtype: Any):
        space = self.model.observation_space.spaces[key]
        size = int(self.np.prod(space.shape))
        source = self.np.asarray(values if values is not None else [], dtype=dtype).reshape(-1)
        result = self.np.zeros(size, dtype=dtype)
        count = min(size, source.size)
        if count:
            result[:count] = source[:count]
        return result.reshape(space.shape)

    def _remap_ids(self, values, mapping: dict[int, int]):
        result = self.np.zeros_like(values)
        for current, old in mapping.items():
            result[values == current] = old
        return result

    def _project_tools(self, values: Any):
        current = self.np.asarray(values if values is not None else [], dtype=self.np.int32).reshape(-1)
        projected = [current[index] if index is not None and index < len(current) else 0 for index in self.tool_indices]
        return self._values(projected, "tool_counts", self.np.int32)


def action_key(action: dict[str, Any]) -> tuple[str, str, tuple[int, ...]]:
    return (
        str(action.get("type", "")),
        str(action.get("cardId", "")),
        tuple(int(index) for index in action.get("targetSlots", [])),
    )


def build_model_projection(model_spec: dict[str, Any] | None, specification: dict[str, Any],
                           model_actions: int) -> dict[str, Any]:
    """Map the current server contract onto the contract stored with a model."""
    current_actions = sorted(specification.get("actions", []), key=lambda item: int(item.get("index", 0)))
    if model_spec is None:
        indices = [index if index < len(current_actions) else None for index in range(model_actions)]
        return {
            "action_indices": indices,
            "actions": {i: current_actions[index]["action"] for i, index in enumerate(indices) if index is not None},
            "card_id_map": {},
            "monster_id_map": {},
            "tool_indices": list(range(len(specification.get("toolIds", [])))),
        }

    old_actions = sorted(model_spec.get("actions", []), key=lambda item: int(item.get("index", 0)))
    current_by_key = {action_key(item.get("action", {})): (index, item.get("action", {}))
                      for index, item in enumerate(current_actions)}
    action_indices: list[int | None] = []
    actions: dict[int, dict[str, Any]] = {}
    for model_index in range(model_actions):
        old = old_actions[model_index].get("action", {}) if model_index < len(old_actions) else None
        found = current_by_key.get(action_key(old)) if old is not None else None
        action_indices.append(found[0] if found else None)
        if found:
            actions[model_index] = found[1]

    def id_map(key: str) -> dict[int, int]:
        old_positions = {str(value): index + 1 for index, value in enumerate(model_spec.get(key, []))}
        return {index + 1: old_positions.get(str(value), 0)
                for index, value in enumerate(specification.get(key, []))}

    current_tools = {str(value): index for index, value in enumerate(specification.get("toolIds", []))}
    return {
        "action_indices": action_indices,
        "actions": actions,
        "card_id_map": id_map("cardIds"),
        "monster_id_map": id_map("monsterIds"),
        "tool_indices": [current_tools.get(str(value)) for value in model_spec.get("toolIds", [])],
    }


def legacy_model_spec(metadata: dict[str, Any], specification: dict[str, Any],
                      model_actions: int) -> dict[str, Any] | None:
    """Recover the only pre-modelSpec artifact format; new artifacts embed their full contract."""
    if metadata.get("specHash") != "38d17150713db69fc6b6adc275bb8b5bde6e806ddc01b146ba5e2353148b51c9":
        return None
    legacy = deepcopy(specification)
    legacy["cardIds"] = [card_id for card_id in legacy.get("cardIds", []) if card_id != "waking_salts"]
    legacy["actions"] = [item for item in legacy.get("actions", [])
                         if item.get("action", {}).get("cardId") != "waking_salts"]
    for index, item in enumerate(legacy["actions"]):
        item["index"] = index
    legacy.setdefault("tensor", {})["actionCount"] = len(legacy["actions"])
    legacy["tensor"]["toolCount"] = len(legacy.get("toolIds", []))
    if len(legacy["actions"]) != model_actions:
        return None
    return legacy
