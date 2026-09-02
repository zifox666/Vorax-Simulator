from __future__ import annotations

import argparse
import json
import os
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import gymnasium as gym
import numpy as np

from .env import VoraxEnv, VoraxVectorEnv
from .preferences import PreferenceTracker
from .reporting import print_score_summary, summarize_diagnostics, summarize_refreshes, summarize_scores
from .rewards import TierRewardConfig, preference_weight_at


class ScaledRewardEnv(gym.Wrapper):
    """Scale large game scores for PPO while preserving the real score in info."""

    def __init__(self, env: VoraxEnv, scale: float):
        if scale <= 0:
            raise ValueError("reward scale must be positive")
        super().__init__(env)
        self.scale = scale

    def step(self, action: int):
        observation, reward, terminated, truncated, info = self.env.step(action)
        info["raw_reward"] = reward
        return observation, reward / self.scale, terminated, truncated, info

    def action_masks(self) -> np.ndarray:
        return self.env.action_masks()


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description="在 macOS 或其他平台上训练渴瘾模拟器 MaskablePPO 模型")
    result.add_argument("--server", default=os.getenv("VORAX_SERVER", "http://127.0.0.1:8080"), help="模拟器服务地址")
    result.add_argument("--api-key", default=os.getenv("VORAX_TRAINING_KEY", ""), help="训练 API Key；推荐通过 VORAX_TRAINING_KEY 设置")
    result.add_argument("--timesteps", type=int, default=200_000, help="本次训练步数")
    result.add_argument("--envs", type=int, default=16, help="通过批量 API 同步训练的环境数量（1–256）")
    result.add_argument("--output", default="models/vorax-maskable-ppo", help="模型输出路径，不需要 .zip 后缀")
    result.add_argument("--resume", help="已有 MaskablePPO 模型路径，用于续训")
    result.add_argument("--seed", type=int, default=42, help="PPO 与环境起始随机种子")
    result.add_argument("--pet-refreshes", type=int, choices=(0, 1, 2), default=0, help="每局宠物提供的用具刷新次数")
    result.add_argument("--reward-mode", choices=("tier", "score"), default="tier", help="tier 按档位训练；score 保留旧的原始分数模式")
    result.add_argument("--reward-scale", type=float, default=10_000, help="score 奖励模式的缩放除数")
    result.add_argument("--floor-score", type=int, default=607_000, help="保底分数门槛")
    result.add_argument("--excellent-score", type=int, default=721_000, help="优秀分数门槛")
    result.add_argument("--score-cap", type=int, default=1_120_000, help="有效分数上限，超过后不再增加训练奖励")
    result.add_argument("--floor-bonus", type=float, default=6.0, help="跨过保底门槛的奖励跳跃；用于优先提高保底率")
    result.add_argument("--excellent-bonus", type=float, default=2.0, help="跨过优秀门槛的额外奖励跳跃")
    result.add_argument(
        "--preference-weight",
        type=float,
        default=0.05,
        help="训练开始时的流派攻略奖励权重；0 关闭",
    )
    result.add_argument("--preference-final-weight", type=float, default=0.0, help="流派攻略衰减后的最终权重")
    result.add_argument("--preference-decay-fraction", type=float, default=0.4, help="用本次训练前多少比例线性衰减流派权重")
    result.add_argument(
        "--foundation-refresh-weight",
        type=float,
        default=0.1,
        help="开局锁用具及前四瓶合理刷新奖励权重；0 关闭",
    )
    result.add_argument(
        "--observation-encoder",
        choices=("legacy", "categorical"),
        default="categorical",
        help="categorical 对类别做独热编码并归一化大数值；legacy 仅用于兼容旧模型",
    )
    result.add_argument("--device", choices=("auto", "cpu", "mps"), default="auto", help="auto 在 Apple Silicon 可用时选择 MPS，否则 CPU")
    result.add_argument("--n-steps", type=int, default=128, help="每个环境每次 PPO rollout 的步数")
    result.add_argument("--batch-size", type=int, default=256, help="PPO mini-batch 大小")
    result.add_argument("--learning-rate", type=float, default=1e-4)
    result.add_argument("--n-epochs", type=int, default=5, help="每批 rollout 的 PPO 重复训练轮数")
    result.add_argument("--clip-range", type=float, default=0.15, help="PPO 裁剪范围")
    result.add_argument("--target-kl", type=float, default=0.02, help="近似 KL 超过目标后提前停止本轮更新；0 关闭")
    result.add_argument("--ent-coef", type=float, default=0.003, help="PPO 熵系数，用于避免策略过早固化")
    result.add_argument("--tensorboard-log", default="runs", help="TensorBoard 日志目录；留空关闭")
    result.add_argument("--tui", action="store_true", help="在终端显示实时训练与环境状态仪表盘")
    result.add_argument("--tui-refresh", type=float, default=1.0, help="TUI 最短重绘间隔（秒，默认 1）")
    result.add_argument("--evaluate", type=int, default=10, help="训练后确定性评估局数；0 表示跳过")
    result.add_argument("--eval-envs", type=int, default=64, help="训练后评估的并行环境数（1–256）")
    result.add_argument("--target-floor-rate", type=float, default=0.8, help="训练后报告的目标保底率")
    result.add_argument("--target-excellent-rate", type=float, default=0.5, help="训练后报告的目标优秀率")
    result.add_argument(
        "--report",
        type=Path,
        help="训练后完整诊断报告路径；默认 reports/<模型名>-seed<seed>.json",
    )
    return result


def choose_device(requested: str, torch: Any) -> str:
    if requested != "auto":
        if requested == "mps" and not torch.backends.mps.is_available():
            raise RuntimeError("当前 PyTorch 无法使用 Apple MPS；请改用 --device cpu")
        return requested
    return "mps" if torch.backends.mps.is_available() else "cpu"


def build_sb3_vector_env(vec_env_class: Any, args: argparse.Namespace):
    reward_mode = getattr(args, "reward_mode", "score")
    tier_reward = TierRewardConfig(
        getattr(args, "floor_score", 607_000),
        getattr(args, "excellent_score", 721_000),
        getattr(args, "score_cap", 1_120_000),
        getattr(args, "floor_bonus", 6.0),
        getattr(args, "excellent_bonus", 2.0),
    )
    initial_preference_weight = float(getattr(args, "preference_weight", 0.0))
    final_preference_weight = min(float(getattr(args, "preference_final_weight", initial_preference_weight)), initial_preference_weight)
    preference_decay_fraction = float(getattr(args, "preference_decay_fraction", 1.0))
    foundation_refresh_weight = float(getattr(args, "foundation_refresh_weight", 0.0))

    class RemoteBatchVecEnv(vec_env_class):
        def __init__(self):
            self.remote = VoraxVectorEnv(args.server, args.api_key, args.envs, pet_refreshes=args.pet_refreshes)
            super().__init__(args.envs, self.remote.single_observation_space, self.remote.single_action_space)
            self._actions: np.ndarray | None = None
            self._next_seeds: list[int | None] = [args.seed + index for index in range(args.envs)]
            self._last_observation: dict[str, np.ndarray] | None = None
            self._last_infos: list[dict[str, Any]] | None = None
            self._preference_trackers = [PreferenceTracker() for _ in range(args.envs)]
            self._trained_timesteps = 0

        def preference_weight(self) -> float:
            return preference_weight_at(
                initial_preference_weight,
                final_preference_weight,
                self._trained_timesteps,
                args.timesteps,
                preference_decay_fraction,
            )

        def reset(self):
            observation, infos = self.remote.reset(seed=self._next_seeds)
            self._next_seeds = [None] * self.num_envs
            self.reset_infos = _split_infos(infos, self.num_envs)
            self._last_infos = self.reset_infos
            self._last_observation = observation
            return observation

        def step_async(self, actions):
            requested = np.asarray(actions, dtype=np.int64)
            if requested.shape != (self.num_envs,):
                raise ValueError(f"actions must have shape ({self.num_envs},)")
            masks = self.action_masks()
            invalid = np.flatnonzero(
                (requested < 0)
                | (requested >= self.remote.single_action_space.n)
                | ~masks[np.arange(self.num_envs), np.clip(requested, 0, self.remote.single_action_space.n - 1)]
            )
            if invalid.size:
                details = ", ".join(
                    f"env={index}, action={requested[index]}, legal={np.flatnonzero(masks[index]).tolist()}"
                    for index in invalid
                )
                raise RuntimeError(f"policy selected an action outside the client action mask: {details}")
            self._actions = requested

        def step_wait(self):
            if self._actions is None:
                raise RuntimeError("step_async() must be called before step_wait()")
            if self._last_infos is None:
                raise RuntimeError("environment has not been reset")
            actions = self._actions.copy()
            previous_scores = np.asarray([int(info["score"]) for info in self._last_infos], dtype=np.int64)
            guide_points = np.asarray(
                [
                    tracker.advantage(
                        info["semantic_observation"],
                        self.remote.specification["actions"][int(action)]["action"],
                        list(info["legal_actions"]),
                    )
                    for tracker, info, action in zip(self._preference_trackers, self._last_infos, actions, strict=True)
                ],
                dtype=np.float64,
            )
            foundation_points = np.asarray(
                [
                    tracker.foundation(
                        info["semantic_observation"],
                        self.remote.specification["actions"][int(action)]["action"],
                    )
                    for tracker, info, action in zip(self._preference_trackers, self._last_infos, actions, strict=True)
                ],
                dtype=np.float64,
            )
            observation, raw_rewards, terminated, truncated, infos = self.remote.step(actions)
            self._actions = None
            dones = np.logical_or(terminated, truncated)
            split_infos = _split_infos(infos, self.num_envs)
            next_scores = np.asarray([int(info["score"]) for info in split_infos], dtype=np.int64)
            if reward_mode == "tier":
                objective_rewards = np.asarray(
                    [tier_reward.transition_reward(before, after) for before, after in zip(previous_scores, next_scores, strict=True)],
                    dtype=np.float64,
                )
            else:
                objective_rewards = raw_rewards / args.reward_scale
            guide_weight = self.preference_weight()
            preference_rewards = guide_weight * guide_points
            foundation_rewards = foundation_refresh_weight * foundation_points
            for index in range(self.num_envs):
                split_infos[index]["raw_reward"] = float(raw_rewards[index])
                split_infos[index]["objective_reward"] = float(objective_rewards[index])
                split_infos[index]["score_utility"] = float(tier_reward.utility(next_scores[index]))
                split_infos[index]["preference_points"] = float(guide_points[index])
                split_infos[index]["preference_weight"] = guide_weight
                split_infos[index]["preference_reward"] = float(preference_rewards[index])
                split_infos[index]["foundation_points"] = float(foundation_points[index])
                split_infos[index]["foundation_reward"] = float(foundation_rewards[index])
                split_infos[index]["playbook"] = self._preference_trackers[index].name
                split_infos[index]["TimeLimit.truncated"] = bool(truncated[index] and not terminated[index])
                if dones[index]:
                    split_infos[index]["terminal_observation"] = {key: value[index].copy() for key, value in observation.items()}
            if dones.any():
                observation, reset_infos = self.remote.reset(options={"reset_mask": dones})
                reset_split = _split_infos(reset_infos, self.num_envs)
                for index in np.flatnonzero(dones):
                    self.reset_infos[index] = reset_split[index]
                    self._preference_trackers[index] = PreferenceTracker()
            self._last_infos = _split_infos(reset_infos, self.num_envs) if dones.any() else split_infos
            if dones.any():
                for index in np.flatnonzero(~dones):
                    self._last_infos[index] = split_infos[index]
            self._last_observation = observation
            self._trained_timesteps += self.num_envs
            rewards = objective_rewards + preference_rewards + foundation_rewards
            return observation, rewards, dones, split_infos

        def action_masks(self):
            if self._last_observation is None:
                raise RuntimeError("environment has not been reset")
            return self._last_observation["action_mask"].astype(np.bool_, copy=True)

        def seed(self, seed=None):
            base = args.seed if seed is None else int(seed)
            self._next_seeds = [base + index for index in range(self.num_envs)]
            return self._next_seeds.copy()

        def close(self):
            self.remote.close()

        def get_attr(self, attr_name, indices=None):
            selected = _indices(indices, self.num_envs)
            if attr_name == "render_mode":
                return [None for _ in selected]
            # sb3-contrib probes VecEnv support via get_attr() before it calls
            # env_method("action_masks").  Expose the callable for every
            # logical environment so that probe reflects the implementation
            # below rather than the remote batch client's attributes.
            if attr_name == "action_masks":
                return [self.action_masks for _ in selected]
            return [getattr(self.remote, attr_name) for _ in selected]

        def set_attr(self, attr_name, value, indices=None):
            setattr(self.remote, attr_name, value)

        def env_method(self, method_name, *method_args, indices=None, **method_kwargs):
            selected = _indices(indices, self.num_envs)
            if method_name == "action_masks":
                masks = self.action_masks()
                return [masks[index] for index in selected]
            method = getattr(self.remote, method_name)
            return [method(*method_args, **method_kwargs) for _ in selected]

        def env_is_wrapped(self, wrapper_class, indices=None):
            return [False for _ in _indices(indices, self.num_envs)]

    return RemoteBatchVecEnv()


def _indices(indices: Any, size: int) -> list[int]:
    if indices is None:
        return list(range(size))
    if isinstance(indices, (int, np.integer)):
        return [int(indices)]
    return [int(index) for index in indices]


def _split_infos(infos: dict[str, np.ndarray], size: int) -> list[dict[str, Any]]:
    return [{key: values[index] for key, values in infos.items()} for index in range(size)]


def evaluation_report_path(output: Path, seed: int, requested: Path | None = None) -> Path:
    """Resolve the automatic post-training report without changing the model path."""
    return requested or Path("reports") / f"{output.name}-seed{seed}.json"


def evaluate(model: Any, env: ScaledRewardEnv, episodes: int, seed: int) -> list[int]:
    scores: list[int] = []
    for episode in range(episodes):
        observation, info = env.reset(seed=seed + 100_000 + episode)
        terminated = truncated = False
        while not (terminated or truncated):
            action, _ = model.predict(observation, action_masks=env.action_masks(), deterministic=True)
            observation, _, terminated, truncated, info = env.step(int(action))
        scores.append(int(info["score"]))
    return scores


def evaluate_vectorized(model: Any, env: VoraxVectorEnv, episodes: int, seed: int) -> list[int]:
    """Evaluate fixed seeds concurrently while preserving score order."""
    scores, _, _ = evaluate_vectorized_with_diagnostics(model, env, episodes, seed)
    return scores


def evaluate_vectorized_detailed(
    model: Any,
    env: VoraxVectorEnv,
    episodes: int,
    seed: int,
) -> tuple[list[int], dict[str, list[int]]]:
    """Evaluate fixed seeds and retain per-episode refresh usage."""
    scores, refreshes, _ = evaluate_vectorized_with_diagnostics(model, env, episodes, seed)
    return scores, refreshes


def evaluate_vectorized_with_diagnostics(
    model: Any,
    env: VoraxVectorEnv,
    episodes: int,
    seed: int,
) -> tuple[list[int], dict[str, list[int]], list[dict[str, Any]]]:
    """Evaluate fixed seeds and retain enough state to diagnose failed builds."""
    if episodes < 1:
        raise ValueError("episodes must be positive")
    if env.num_envs > episodes:
        raise ValueError("evaluation environment count cannot exceed episodes")

    results: list[int | None] = [None] * episodes
    refreshes = {
        "potion": [0] * episodes,
        "tool": [0] * episodes,
        "earlyPotion": [0] * episodes,
        "openingTool": [0] * episodes,
    }
    diagnostics: list[dict[str, Any]] = [
        {
            "episode": episode,
            "seed": seed + 100_000 + episode,
            "initialPlaybook": "未匹配攻略流派",
            "expectedOpeningTool": "",
            "actualOpeningTool": "",
            "openingToolMatched": False,
            "rewardTools": [],
            "potions": [],
            "schemes": [],
            "guideDeviationCount": 0,
            "foundationMistakeCount": 0,
            "actions": [],
        }
        for episode in range(episodes)
    ]
    next_episode = 0
    lane_episodes: list[int | None] = []
    initial_seeds: list[int] = []
    for _ in range(env.num_envs):
        lane_episodes.append(next_episode)
        initial_seeds.append(seed + 100_000 + next_episode)
        next_episode += 1
    observation, infos = env.reset(seed=initial_seeds)
    trackers = [PreferenceTracker() for _ in range(env.num_envs)]
    dummy_seed = seed + 100_000 + episodes

    while any(score is None for score in results):
        masks = observation["action_mask"].astype(np.bool_, copy=False)
        actions, _ = model.predict(observation, action_masks=masks, deterministic=True)
        actions = np.asarray(actions, dtype=np.int64)
        for lane, raw_action in enumerate(actions):
            episode = lane_episodes[lane]
            if episode is None:
                continue
            action_definition = env.specification["actions"][int(raw_action)]["action"]
            action = {
                key: list(value) if isinstance(value, list) else value
                for key, value in action_definition.items()
                if value not in (None, "", [])
            }
            semantic = infos["semantic_observation"][lane]
            legal_actions = list(infos["legal_actions"][lane])
            tracker = trackers[lane]
            guide_advantage = float(tracker.advantage(semantic, action, legal_actions))
            foundation_points = float(tracker.foundation(semantic, action))
            row = diagnostics[episode]
            if tracker.playbook is not None:
                row["initialPlaybook"] = tracker.name
                row["expectedOpeningTool"] = tracker.playbook.opening_tool
            if guide_advantage < -0.05:
                row["guideDeviationCount"] += 1
            if foundation_points < 0:
                row["foundationMistakeCount"] += 1

            offer_kind = int(observation["offer"][lane][0])
            reward_threshold = int(observation["offer_reward_threshold"][lane][0])
            base_cursor = int(observation["progress"][lane][0])
            action_type = str(action.get("type", ""))
            trace = {
                "step": len(row["actions"]),
                "stage": semantic.get("stageLabel", ""),
                "baseCursor": base_cursor,
                "scoreBefore": int(semantic.get("score", 0)),
                "offerKind": offer_kind,
                "rewardThreshold": reward_threshold,
                "action": action,
                "guideAdvantage": guide_advantage,
                "foundationPoints": foundation_points,
            }
            row["actions"].append(trace)

            if action_type == "refresh":
                if offer_kind == 2:
                    refreshes["potion"][episode] += 1
                    if base_cursor <= 4:
                        refreshes["earlyPotion"][episode] += 1
                elif offer_kind == 3:
                    refreshes["tool"][episode] += 1
                    if reward_threshold == 0:
                        refreshes["openingTool"][episode] += 1
            elif action_type == "choose":
                choice = {
                    "cardId": str(action.get("cardId", "")),
                    "targetSlots": list(action.get("targetSlots", [])),
                    "baseCursor": base_cursor,
                }
                if offer_kind == 3 and reward_threshold == 0:
                    row["actualOpeningTool"] = choice["cardId"]
                elif offer_kind == 3:
                    row["rewardTools"].append({**choice, "threshold": reward_threshold})
                elif offer_kind == 2:
                    row["potions"].append(choice)
                elif offer_kind == 4:
                    row["schemes"].append(choice)

        observation, _, terminated, truncated, next_infos = env.step(actions)
        dones = np.logical_or(terminated, truncated)
        if not dones.any():
            infos = next_infos
            continue

        for lane in np.flatnonzero(dones):
            episode = lane_episodes[lane]
            if episode is not None:
                score = int(next_infos["score"][lane])
                results[episode] = score
                terminal = next_infos["semantic_observation"][lane]
                row = diagnostics[episode]
                row["score"] = score
                row["openingToolMatched"] = bool(
                    row["expectedOpeningTool"]
                    and row["actualOpeningTool"] == row["expectedOpeningTool"]
                )
                row["finalTools"] = list(terminal.get("tools", []))
                row["finalSlots"] = [dict(slot) for slot in terminal.get("slots", [])]
                row["refreshes"] = {key: values[episode] for key, values in refreshes.items()}
        if all(score is not None for score in results):
            break

        reset_seeds: list[int | None] = [None] * env.num_envs
        for lane in np.flatnonzero(dones):
            if next_episode < episodes:
                lane_episodes[lane] = next_episode
                reset_seeds[lane] = seed + 100_000 + next_episode
                next_episode += 1
            else:
                # Keep the batch API full while slower real episodes finish.
                lane_episodes[lane] = None
                reset_seeds[lane] = dummy_seed
                dummy_seed += 1
            trackers[lane] = PreferenceTracker()
        observation, infos = env.reset(seed=reset_seeds, options={"reset_mask": dones})

    return [int(score) for score in results if score is not None], refreshes, diagnostics


def main() -> None:
    args = parser().parse_args()
    if not args.api_key:
        raise SystemExit("缺少训练 API Key：请设置 VORAX_TRAINING_KEY 或传入 --api-key")
    if (
        args.timesteps < 1
        or args.n_steps < 2
        or args.batch_size < 2
        or args.envs < 1
        or args.envs > 256
        or args.eval_envs < 1
        or args.eval_envs > 256
        or args.tui_refresh < 0.1
    ):
        raise SystemExit("timesteps、n-steps 和 batch-size 必须为正数")
    if not 0 <= args.target_floor_rate <= 1 or not 0 <= args.target_excellent_rate <= 1:
        raise SystemExit("目标保底率和优秀率必须在 0–1")
    if (
        args.reward_scale <= 0
        or args.preference_weight < 0
        or args.preference_final_weight < 0
        or args.foundation_refresh_weight < 0
    ):
        raise SystemExit("reward-scale 必须为正数，流派及刷新权重不能为负数")
    if args.preference_final_weight > args.preference_weight or not 0 < args.preference_decay_fraction <= 1:
        raise SystemExit("流派最终权重不能大于初始权重，衰减比例必须在 (0, 1] 内")
    if args.ent_coef < 0 or args.n_epochs < 1 or args.clip_range <= 0 or args.target_kl < 0:
        raise SystemExit("ent-coef/target-kl 不能为负数，n-epochs 和 clip-range 必须为正数")
    try:
        TierRewardConfig(
            args.floor_score,
            args.excellent_score,
            args.score_cap,
            args.floor_bonus,
            args.excellent_bonus,
        )
    except ValueError as error:
        raise SystemExit("分数档位和奖励权重无效：需满足门槛递增、floor-bonus > 0、excellent-bonus >= 0") from error
    if args.n_steps * args.envs % args.batch_size != 0:
        raise SystemExit("--n-steps × --envs 必须能被 --batch-size 整除")
    planned_rollouts = int(np.ceil(args.timesteps / (args.n_steps * args.envs)))
    if planned_rollouts < 100:
        recommended_n_steps = max(2, args.timesteps // (args.envs * 100))
        print(
            f"警告：当前配置预计只有 {planned_rollouts} 次 PPO rollout，策略迭代过少；"
            f"建议把 --n-steps 降到 {recommended_n_steps} 以下，或减少 --envs。"
        )

    try:
        import torch
        from sb3_contrib import MaskablePPO
        from stable_baselines3.common.vec_env import VecEnv

        from .policy import MPSMaskableMultiInputPolicy, VoraxFeaturesExtractor
        from .tui import TrainingDashboard
    except ImportError as error:
        raise SystemExit("缺少训练依赖，请运行：uv sync --extra train") from error

    device = choose_device(args.device, torch)
    env = build_sb3_vector_env(VecEnv, args)
    specification = env.remote.specification
    output = Path(args.output).expanduser()
    output.parent.mkdir(parents=True, exist_ok=True)

    if args.resume:
        model = MaskablePPO.load(
            Path(args.resume).expanduser(),
            env=env,
            device=device,
            n_steps=args.n_steps,
            batch_size=args.batch_size,
            learning_rate=args.learning_rate,
            n_epochs=args.n_epochs,
            clip_range=args.clip_range,
            target_kl=args.target_kl or None,
            ent_coef=args.ent_coef,
            tensorboard_log=args.tensorboard_log or None,
        )
        if device == "mps":
            model.policy.__class__ = MPSMaskableMultiInputPolicy
        reset_timesteps = False
    else:
        policy_kwargs: dict[str, Any] = {"net_arch": [256, 256]}
        if args.observation_encoder == "categorical":
            policy_kwargs.update(
                features_extractor_class=VoraxFeaturesExtractor,
                features_extractor_kwargs={"score_cap": args.score_cap},
            )
        model = MaskablePPO(
            MPSMaskableMultiInputPolicy,
            env,
            seed=args.seed,
            device=device,
            verbose=0 if args.tui else 1,
            n_steps=args.n_steps,
            batch_size=args.batch_size,
            learning_rate=args.learning_rate,
            n_epochs=args.n_epochs,
            clip_range=args.clip_range,
            target_kl=args.target_kl or None,
            gamma=1.0,
            gae_lambda=0.95,
            ent_coef=args.ent_coef,
            tensorboard_log=args.tensorboard_log or None,
            policy_kwargs=policy_kwargs,
        )
        reset_timesteps = True

    actual_observation_encoder = (
        "categorical" if isinstance(model.policy.features_extractor, VoraxFeaturesExtractor) else "legacy"
    )
    if args.resume and actual_observation_encoder != args.observation_encoder:
        print(
            f"提示：续训模型实际使用 {actual_observation_encoder} 编码，"
            f"忽略命令行的 --observation-encoder {args.observation_encoder}。"
        )

    start_timesteps = model.num_timesteps
    if args.tui:
        model.verbose = 0
    print(f"连接 {args.server}，envs={args.envs}，device={device}，开始训练 {args.timesteps:,} 步")
    dashboard = (
        TrainingDashboard(
            args.timesteps,
            args.envs,
            device,
            start_timesteps=start_timesteps,
            refresh_seconds=args.tui_refresh,
            floor_score=args.floor_score,
            excellent_score=args.excellent_score,
            score_cap=args.score_cap,
        )
        if args.tui
        else None
    )
    try:
        model.learn(total_timesteps=args.timesteps, reset_num_timesteps=reset_timesteps, callback=dashboard)
        model.save(str(output))
    finally:
        if dashboard is not None:
            dashboard.stop()
        env.close()

    if args.evaluate > 0:
        evaluation_env = VoraxVectorEnv(
            args.server,
            args.api_key,
            min(args.eval_envs, args.evaluate),
            pet_refreshes=args.pet_refreshes,
        )
        try:
            scores, evaluation_refreshes, evaluation_diagnostics = evaluate_vectorized_with_diagnostics(
                model, evaluation_env, args.evaluate, args.seed
            )
        finally:
            evaluation_env.close()
    else:
        scores = []
        evaluation_refreshes = {"potion": [], "tool": [], "earlyPotion": [], "openingTool": []}
        evaluation_diagnostics = []

    metadata = {
        "createdAt": datetime.now(UTC).isoformat(),
        "server": args.server,
        "timesteps": args.timesteps,
        "environments": args.envs,
        "seed": args.seed,
        "petRefreshes": args.pet_refreshes,
        "rewardScale": args.reward_scale,
        "rewardMode": args.reward_mode,
        "scoreTiers": {"floor": args.floor_score, "excellent": args.excellent_score, "cap": args.score_cap},
        "tierBonuses": {"floor": args.floor_bonus, "excellent": args.excellent_bonus},
        "preferenceWeight": args.preference_weight,
        "preferenceFinalWeight": args.preference_final_weight,
        "preferenceDecayFraction": args.preference_decay_fraction,
        "foundationRefreshWeight": args.foundation_refresh_weight,
        "preferenceProfile": "training/流派偏好.md",
        "preferenceNormalization": "legal-action-centered-range",
        "entropyCoefficient": args.ent_coef,
        "ppo": {
            "learningRate": args.learning_rate,
            "nSteps": args.n_steps,
            "batchSize": args.batch_size,
            "nEpochs": args.n_epochs,
            "clipRange": args.clip_range,
            "targetKL": args.target_kl,
            "plannedRollouts": planned_rollouts,
        },
        "observationEncoder": actual_observation_encoder,
        "device": device,
        "specVersion": specification["specVersion"],
        "specHash": specification["specHash"],
        "rulesVersion": specification["rulesVersion"],
        "contentVersion": specification["contentVersion"],
        "evaluationScores": scores,
    }
    output.with_suffix(".json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"模型已保存：{output.with_suffix('.zip')}")
    if scores:
        summary = summarize_scores(scores, args.floor_score, args.excellent_score, args.score_cap)
        refresh_summary = summarize_refreshes(evaluation_refreshes)
        diagnostic_summary = summarize_diagnostics(
            evaluation_diagnostics, args.floor_score, args.excellent_score
        )
        requirements = {
            "targetFloorRate": args.target_floor_rate,
            "targetExcellentRate": args.target_excellent_rate,
            "floorPassed": summary["tiers"]["floorOrBetterRate"] >= args.target_floor_rate,
            "excellentPassed": summary["tiers"]["excellentRate"] >= args.target_excellent_rate,
        }
        requirements["usable"] = requirements["floorPassed"] and requirements["excellentPassed"]
        print_score_summary(summary)
        print(
            f"平均刷新：药剂 {refresh_summary['potionMean']:.2f}/3、用具 "
            f"{refresh_summary['toolMean']:.2f}/{args.pet_refreshes}；"
            f"其中前四瓶药剂 {refresh_summary['earlyPotionMean']:.2f}、开局用具 "
            f"{refresh_summary['openingToolMean']:.2f}"
        )
        failure_signals = diagnostic_summary["failureSignals"]
        print(
            f"失败诊断：开局用具错配 {failure_signals['failedOpeningToolMismatch']} 局；"
            f"失败局平均攻略偏离 {failure_signals['failedMeanGuideDeviations']:.2f} 次、"
            f"基础动作错误 {failure_signals['failedMeanFoundationMistakes']:.2f} 次"
        )
        print(
            f"可用性门槛：保底率 ≥ {args.target_floor_rate:.0%}、"
            f"优秀率 ≥ {args.target_excellent_rate:.0%}；"
            f"结果：{'通过' if requirements['usable'] else '未通过'}"
        )
        report_path = evaluation_report_path(output, args.seed, args.report)
        report_path.parent.mkdir(parents=True, exist_ok=True)
        report = {
            "model": str(output.with_suffix(".zip")),
            "server": args.server,
            "device": device,
            "seed": args.seed,
            "petRefreshes": args.pet_refreshes,
            "scores": scores,
            "summary": summary,
            "refreshes": {"perEpisode": evaluation_refreshes, "summary": refresh_summary},
            "diagnostics": {"summary": diagnostic_summary, "episodes": evaluation_diagnostics},
            "requirements": requirements,
            "training": metadata,
        }
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"完整训练与评估诊断已写入：{report_path}")


if __name__ == "__main__":
    main()
