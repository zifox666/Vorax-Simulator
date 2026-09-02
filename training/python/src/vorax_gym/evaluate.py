"""Evaluate a saved MaskablePPO model without changing its weights."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
from typing import Any

from .env import VoraxVectorEnv
from .reporting import print_score_summary, summarize_diagnostics, summarize_refreshes, summarize_scores
from .rewards import TierRewardConfig
from .train import choose_device, evaluate_vectorized_with_diagnostics

# Backward-compatible name used by existing callers and tests.
print_summary = print_score_summary


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description="评估已保存的渴瘾 MaskablePPO 模型，不会继续训练")
    result.add_argument("--model", default="models/vorax-maskable-ppo.zip", help="待评估的 .zip 模型路径")
    result.add_argument("--server", default=os.getenv("VORAX_SERVER", "http://127.0.0.1:8080"), help="模拟器服务地址")
    result.add_argument("--api-key", default=os.getenv("VORAX_TRAINING_KEY", ""), help="训练 API Key；推荐通过 VORAX_TRAINING_KEY 设置")
    result.add_argument("--episodes", type=int, default=100, help="评估局数")
    result.add_argument("--envs", type=int, default=64, help="批量并行评估环境数（1–256，不超过 episodes）")
    result.add_argument("--seed", type=int, default=42, help="评估种子基数；实际评估从 seed + 100000 开始")
    result.add_argument("--pet-refreshes", type=int, choices=(0, 1, 2), default=0, help="每局宠物提供的用具刷新次数")
    result.add_argument("--reward-scale", type=float, default=10_000, help="须与训练时一致；只影响环境奖励，不影响最终分数")
    result.add_argument("--floor-score", type=int, default=607_000, help="保底分数门槛")
    result.add_argument("--excellent-score", type=int, default=721_000, help="优秀分数门槛")
    result.add_argument("--score-cap", type=int, default=1_120_000, help="有效分数上限")
    result.add_argument("--target-floor-rate", type=float, default=0.8, help="模型可用所需最低保底率")
    result.add_argument("--target-excellent-rate", type=float, default=0.5, help="模型可用所需最低优秀率")
    result.add_argument("--device", choices=("auto", "cpu", "mps"), default="auto", help="模型推理设备")
    result.add_argument("--report", type=Path, help="可选：把完整分数、逐局动作与失败诊断写入 JSON")
    return result


def main() -> None:
    args = parser().parse_args()
    if not args.api_key:
        raise SystemExit("缺少训练 API Key：请设置 VORAX_TRAINING_KEY 或传入 --api-key")
    if args.episodes < 1 or args.envs < 1 or args.envs > 256:
        raise SystemExit("--episodes 必须至少为 1，--envs 必须在 1–256")
    if args.reward_scale <= 0:
        raise SystemExit("--reward-scale 必须为正数")
    if not 0 <= args.target_floor_rate <= 1 or not 0 <= args.target_excellent_rate <= 1:
        raise SystemExit("目标保底率和优秀率必须在 0–1")
    try:
        TierRewardConfig(args.floor_score, args.excellent_score, args.score_cap)
    except ValueError as error:
        raise SystemExit("分数档位必须满足 0 < floor-score < excellent-score < score-cap") from error

    model_path = args.model.expanduser() if isinstance(args.model, Path) else Path(args.model).expanduser()
    if not model_path.is_file():
        raise SystemExit(f"找不到模型：{model_path}")

    try:
        import torch
        from sb3_contrib import MaskablePPO

        from .policy import MPSMaskableMultiInputPolicy
    except ImportError as error:
        raise SystemExit("缺少训练依赖，请运行：uv sync --project training/python --extra train") from error

    device = choose_device(args.device, torch)
    model: Any = MaskablePPO.load(str(model_path), device=device)
    if device == "mps":
        model.policy.__class__ = MPSMaskableMultiInputPolicy

    environment = VoraxVectorEnv(
        args.server,
        args.api_key,
        min(args.envs, args.episodes),
        pet_refreshes=args.pet_refreshes,
    )
    try:
        scores, refreshes, diagnostics = evaluate_vectorized_with_diagnostics(
            model, environment, args.episodes, args.seed
        )
    finally:
        environment.close()

    summary = summarize_scores(scores, args.floor_score, args.excellent_score, args.score_cap)
    refresh_summary = summarize_refreshes(refreshes)
    diagnostic_summary = summarize_diagnostics(diagnostics, args.floor_score, args.excellent_score)
    requirements = {
        "targetFloorRate": args.target_floor_rate,
        "targetExcellentRate": args.target_excellent_rate,
        "floorPassed": summary["tiers"]["floorOrBetterRate"] >= args.target_floor_rate,
        "excellentPassed": summary["tiers"]["excellentRate"] >= args.target_excellent_rate,
    }
    requirements["usable"] = requirements["floorPassed"] and requirements["excellentPassed"]
    print(f"模型：{model_path}；device={device}；pet-refreshes={args.pet_refreshes}")
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
        f"可用性门槛：保底率 ≥ {args.target_floor_rate:.0%}、优秀率 ≥ {args.target_excellent_rate:.0%}；"
        f"结果：{'通过' if requirements['usable'] else '未通过'}"
    )

    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        report = {
            "model": str(model_path),
            "server": args.server,
            "device": device,
            "seed": args.seed,
            "petRefreshes": args.pet_refreshes,
            "scores": scores,
            "summary": summary,
            "refreshes": {"perEpisode": refreshes, "summary": refresh_summary},
            "diagnostics": {"summary": diagnostic_summary, "episodes": diagnostics},
            "requirements": requirements,
        }
        args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"完整报告已写入：{args.report}")


if __name__ == "__main__":
    main()
