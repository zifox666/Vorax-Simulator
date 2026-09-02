"""Evaluate the handbook preference scorer as a deterministic greedy policy."""

from __future__ import annotations

import argparse
import json
import os
from collections import Counter
from pathlib import Path
from typing import Any

import numpy as np

from .env import VoraxVectorEnv
from .evaluate import print_summary, summarize_scores
from .preferences import PreferenceTracker


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description="直接评估攻略评分的贪心策略，不加载 PPO 模型")
    result.add_argument("--server", default=os.getenv("VORAX_SERVER", "http://127.0.0.1:8080"))
    result.add_argument("--api-key", default=os.getenv("VORAX_TRAINING_KEY", ""))
    result.add_argument("--episodes", type=int, default=1000)
    result.add_argument("--envs", type=int, default=128)
    result.add_argument("--seed", type=int, default=2026)
    result.add_argument("--pet-refreshes", type=int, choices=(0, 1, 2), default=0)
    result.add_argument("--floor-score", type=int, default=607_000)
    result.add_argument("--excellent-score", type=int, default=721_000)
    result.add_argument("--score-cap", type=int, default=1_120_000)
    result.add_argument("--target-floor-rate", type=float, default=0.8)
    result.add_argument("--target-excellent-rate", type=float, default=0.5)
    result.add_argument("--report", type=Path)
    return result


def greedy_guide_actions(
    trackers: list[PreferenceTracker],
    observations: dict[str, np.ndarray],
    infos: dict[str, np.ndarray],
    specification: dict[str, Any],
) -> np.ndarray:
    """Choose the highest raw handbook score among each lane's legal actions."""
    action_table = {
        int(entry["index"]): entry["action"]
        for entry in specification["actions"]
    }
    result = np.zeros(len(trackers), dtype=np.int64)
    for lane, tracker in enumerate(trackers):
        legal_indexes = np.flatnonzero(observations["action_mask"][lane])
        if not len(legal_indexes):
            raise RuntimeError(f"guide evaluation lane {lane} has no legal action")
        semantic = infos["semantic_observation"][lane]
        # max() keeps the first index on ties, making fixed-seed evaluation reproducible.
        result[lane] = max(
            legal_indexes,
            key=lambda index: tracker.score(semantic, action_table[int(index)]),
        )
    return result


def evaluate_greedy_guide(
    env: VoraxVectorEnv,
    episodes: int,
    seed: int,
) -> tuple[list[int], list[str]]:
    if episodes < 1 or env.num_envs > episodes:
        raise ValueError("episodes must be positive and at least the environment count")

    scores: list[int | None] = [None] * episodes
    playbooks: list[str | None] = [None] * episodes
    trackers = [PreferenceTracker() for _ in range(env.num_envs)]
    lane_episodes: list[int | None] = list(range(env.num_envs))
    next_episode = env.num_envs
    initial_seeds = [seed + 100_000 + episode for episode in range(env.num_envs)]
    observations, infos = env.reset(seed=initial_seeds)
    dummy_seed = seed + 100_000 + episodes

    while any(score is None for score in scores):
        actions = greedy_guide_actions(trackers, observations, infos, env.specification)
        observations, _, terminated, truncated, infos = env.step(actions)
        dones = np.logical_or(terminated, truncated)
        if not dones.any():
            continue

        for lane in np.flatnonzero(dones):
            episode = lane_episodes[lane]
            if episode is not None:
                scores[episode] = int(infos["score"][lane])
                playbooks[episode] = trackers[lane].name
        if all(score is not None for score in scores):
            break

        reset_seeds: list[int | None] = [None] * env.num_envs
        for lane in np.flatnonzero(dones):
            trackers[lane] = PreferenceTracker()
            if next_episode < episodes:
                lane_episodes[lane] = next_episode
                reset_seeds[lane] = seed + 100_000 + next_episode
                next_episode += 1
            else:
                lane_episodes[lane] = None
                reset_seeds[lane] = dummy_seed
                dummy_seed += 1
        observations, infos = env.reset(seed=reset_seeds, options={"reset_mask": dones})

    return (
        [int(score) for score in scores if score is not None],
        [str(playbook) for playbook in playbooks if playbook is not None],
    )


def main() -> None:
    args = parser().parse_args()
    if not args.api_key:
        raise SystemExit("缺少训练 API Key：请设置 VORAX_TRAINING_KEY 或传入 --api-key")
    if args.episodes < 1 or args.envs < 1 or args.envs > 256 or args.envs > args.episodes:
        raise SystemExit("--episodes 必须至少为 1，--envs 必须在 1–256 且不能超过 episodes")

    environment = VoraxVectorEnv(
        args.server,
        args.api_key,
        args.envs,
        pet_refreshes=args.pet_refreshes,
    )
    try:
        scores, playbooks = evaluate_greedy_guide(environment, args.episodes, args.seed)
    finally:
        environment.close()

    summary = summarize_scores(scores, args.floor_score, args.excellent_score, args.score_cap)
    requirements = {
        "targetFloorRate": args.target_floor_rate,
        "targetExcellentRate": args.target_excellent_rate,
        "floorPassed": summary["tiers"]["floorOrBetterRate"] >= args.target_floor_rate,
        "excellentPassed": summary["tiers"]["excellentRate"] >= args.target_excellent_rate,
    }
    requirements["usable"] = requirements["floorPassed"] and requirements["excellentPassed"]
    print("策略：攻略评分贪心策略；不使用 PPO 模型")
    print_summary(summary)
    print("流派分布：" + "，".join(f"{name} {count}" for name, count in Counter(playbooks).most_common()))
    print(
        f"可用性门槛：保底率 ≥ {args.target_floor_rate:.0%}、优秀率 ≥ {args.target_excellent_rate:.0%}；"
        f"结果：{'通过' if requirements['usable'] else '未通过'}"
    )

    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        report = {
            "policy": "guide-greedy",
            "server": args.server,
            "seed": args.seed,
            "petRefreshes": args.pet_refreshes,
            "scores": scores,
            "playbooks": dict(Counter(playbooks)),
            "summary": summary,
            "requirements": requirements,
        }
        args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"完整报告已写入：{args.report}")


if __name__ == "__main__":
    main()
