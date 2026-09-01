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
    result.add_argument("--reward-scale", type=float, default=10_000, help="送入 PPO 前的奖励缩放除数")
    result.add_argument("--device", choices=("auto", "cpu", "mps"), default="auto", help="auto 在 Apple Silicon 可用时选择 MPS，否则 CPU")
    result.add_argument("--n-steps", type=int, default=128, help="每个环境每次 PPO rollout 的步数")
    result.add_argument("--batch-size", type=int, default=256, help="PPO mini-batch 大小")
    result.add_argument("--learning-rate", type=float, default=3e-4)
    result.add_argument("--tensorboard-log", default="runs", help="TensorBoard 日志目录；留空关闭")
    result.add_argument("--evaluate", type=int, default=10, help="训练后确定性评估局数；0 表示跳过")
    return result


def choose_device(requested: str, torch: Any) -> str:
    if requested != "auto":
        if requested == "mps" and not torch.backends.mps.is_available():
            raise RuntimeError("当前 PyTorch 无法使用 Apple MPS；请改用 --device cpu")
        return requested
    return "mps" if torch.backends.mps.is_available() else "cpu"


def build_sb3_vector_env(vec_env_class: Any, args: argparse.Namespace):
    class RemoteBatchVecEnv(vec_env_class):
        def __init__(self):
            self.remote = VoraxVectorEnv(args.server, args.api_key, args.envs, pet_refreshes=args.pet_refreshes)
            super().__init__(args.envs, self.remote.single_observation_space, self.remote.single_action_space)
            self._actions: np.ndarray | None = None
            self._next_seeds: list[int | None] = [args.seed + index for index in range(args.envs)]
            self._last_observation: dict[str, np.ndarray] | None = None

        def reset(self):
            observation, infos = self.remote.reset(seed=self._next_seeds)
            self._next_seeds = [None] * self.num_envs
            self.reset_infos = _split_infos(infos, self.num_envs)
            self._last_observation = observation
            return observation

        def step_async(self, actions):
            self._actions = np.asarray(actions, dtype=np.int64)

        def step_wait(self):
            if self._actions is None:
                raise RuntimeError("step_async() must be called before step_wait()")
            observation, raw_rewards, terminated, truncated, infos = self.remote.step(self._actions)
            self._actions = None
            dones = np.logical_or(terminated, truncated)
            split_infos = _split_infos(infos, self.num_envs)
            for index in range(self.num_envs):
                split_infos[index]["raw_reward"] = float(raw_rewards[index])
                split_infos[index]["TimeLimit.truncated"] = bool(truncated[index] and not terminated[index])
                if dones[index]:
                    split_infos[index]["terminal_observation"] = {key: value[index].copy() for key, value in observation.items()}
            if dones.any():
                observation, reset_infos = self.remote.reset(options={"reset_mask": dones})
                reset_split = _split_infos(reset_infos, self.num_envs)
                for index in np.flatnonzero(dones):
                    self.reset_infos[index] = reset_split[index]
            self._last_observation = observation
            return observation, raw_rewards / args.reward_scale, dones, split_infos

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


def main() -> None:
    args = parser().parse_args()
    if not args.api_key:
        raise SystemExit("缺少训练 API Key：请设置 VORAX_TRAINING_KEY 或传入 --api-key")
    if args.timesteps < 1 or args.n_steps < 2 or args.batch_size < 2 or args.envs < 1 or args.envs > 256:
        raise SystemExit("timesteps、n-steps 和 batch-size 必须为正数")
    if args.n_steps * args.envs % args.batch_size != 0:
        raise SystemExit("--n-steps × --envs 必须能被 --batch-size 整除")

    try:
        import torch
        from sb3_contrib import MaskablePPO
        from stable_baselines3.common.vec_env import VecEnv
    except ImportError as error:
        raise SystemExit("缺少训练依赖，请运行：uv sync --extra train") from error

    device = choose_device(args.device, torch)
    env = build_sb3_vector_env(VecEnv, args)
    specification = env.remote.specification
    output = Path(args.output).expanduser()
    output.parent.mkdir(parents=True, exist_ok=True)

    if args.resume:
        model = MaskablePPO.load(Path(args.resume).expanduser(), env=env, device=device)
        reset_timesteps = False
    else:
        model = MaskablePPO(
            "MultiInputPolicy",
            env,
            seed=args.seed,
            device=device,
            verbose=1,
            n_steps=args.n_steps,
            batch_size=args.batch_size,
            learning_rate=args.learning_rate,
            gamma=1.0,
            gae_lambda=0.95,
            tensorboard_log=args.tensorboard_log or None,
            policy_kwargs={"net_arch": [256, 256]},
        )
        reset_timesteps = True

    print(f"连接 {args.server}，envs={args.envs}，device={device}，开始训练 {args.timesteps:,} 步")
    try:
        model.learn(total_timesteps=args.timesteps, reset_num_timesteps=reset_timesteps)
        model.save(str(output))
    finally:
        env.close()

    if args.evaluate > 0:
        evaluation_env = ScaledRewardEnv(VoraxEnv(args.server, args.api_key, pet_refreshes=args.pet_refreshes), args.reward_scale)
        try:
            scores = evaluate(model, evaluation_env, args.evaluate, args.seed)
        finally:
            evaluation_env.close()
    else:
        scores = []

    metadata = {
        "createdAt": datetime.now(UTC).isoformat(),
        "server": args.server,
        "timesteps": args.timesteps,
        "environments": args.envs,
        "seed": args.seed,
        "petRefreshes": args.pet_refreshes,
        "rewardScale": args.reward_scale,
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
        print(f"评估 {len(scores)} 局：平均 {sum(scores)/len(scores):.0f}，最低 {min(scores)}，最高 {max(scores)}")


if __name__ == "__main__":
    main()
