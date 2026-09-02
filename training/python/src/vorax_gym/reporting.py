"""Shared score, refresh, and per-build evaluation reporting."""

from __future__ import annotations

from collections import defaultdict
from typing import Any

import numpy as np

from .rewards import TierRewardConfig


def summarize_scores(
    scores: list[int],
    floor_score: int = 607_000,
    excellent_score: int = 721_000,
    score_cap: int = 1_120_000,
) -> dict[str, Any]:
    if not scores:
        raise ValueError("至少需要一局分数")
    values = np.asarray(scores, dtype=np.float64)
    ordered = np.sort(values)
    trim = int(len(ordered) * 0.1)
    middle = ordered[trim : len(ordered) - trim] if trim else ordered
    tiers = TierRewardConfig(floor_score, excellent_score, score_cap)
    failed = values < tiers.floor_score
    floor = (values >= tiers.floor_score) & (values < tiers.excellent_score)
    excellent = values >= tiers.excellent_score
    capped = values >= tiers.score_cap
    useful_values = np.where(failed, 0, np.minimum(values, tiers.score_cap))
    return {
        "episodes": len(scores),
        "minimum": int(ordered[0]),
        "maximum": int(ordered[-1]),
        "mean": float(values.mean()),
        "variance": float(values.var()),
        "standardDeviation": float(values.std()),
        "p10": float(np.percentile(values, 10)),
        "median": float(np.percentile(values, 50)),
        "p90": float(np.percentile(values, 90)),
        "middle80Mean": float(middle.mean()),
        "tiers": {
            "floorScore": tiers.floor_score,
            "excellentScore": tiers.excellent_score,
            "scoreCap": tiers.score_cap,
            "failed": int(failed.sum()),
            "floor": int(floor.sum()),
            "excellent": int(excellent.sum()),
            "capped": int(capped.sum()),
            "floorOrBetterRate": float((~failed).mean()),
            "excellentRate": float(excellent.mean()),
            "cappedUsefulMean": float(useful_values.mean()),
        },
    }


def summarize_refreshes(refreshes: dict[str, list[int]]) -> dict[str, float]:
    if not refreshes or not refreshes.get("potion"):
        return {"potionMean": 0.0, "toolMean": 0.0, "earlyPotionMean": 0.0, "openingToolMean": 0.0}
    return {
        "potionMean": float(np.mean(refreshes["potion"])),
        "toolMean": float(np.mean(refreshes["tool"])),
        "earlyPotionMean": float(np.mean(refreshes["earlyPotion"])),
        "openingToolMean": float(np.mean(refreshes["openingTool"])),
    }


def _group_summary(rows: list[dict[str, Any]], floor_score: int, excellent_score: int) -> dict[str, Any]:
    scores = [int(row["score"]) for row in rows]
    return {
        "episodes": len(rows),
        "meanScore": float(np.mean(scores)),
        "medianScore": float(np.median(scores)),
        "floorOrBetterRate": float(np.mean(np.asarray(scores) >= floor_score)),
        "excellentRate": float(np.mean(np.asarray(scores) >= excellent_score)),
        "openingToolMatchRate": float(np.mean([bool(row.get("openingToolMatched")) for row in rows])),
        "meanGuideDeviations": float(np.mean([int(row.get("guideDeviationCount", 0)) for row in rows])),
        "meanFoundationMistakes": float(np.mean([int(row.get("foundationMistakeCount", 0)) for row in rows])),
    }


def summarize_diagnostics(
    episodes: list[dict[str, Any]],
    floor_score: int = 607_000,
    excellent_score: int = 721_000,
) -> dict[str, Any]:
    """Aggregate builds and common failure symptoms while retaining raw episodes."""
    by_playbook: dict[str, list[dict[str, Any]]] = defaultdict(list)
    by_opening_tool: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in episodes:
        by_playbook[str(row.get("initialPlaybook") or "未匹配攻略流派")].append(row)
        by_opening_tool[str(row.get("actualOpeningTool") or "未记录")].append(row)

    failed = [row for row in episodes if int(row["score"]) < floor_score]
    succeeded = [row for row in episodes if int(row["score"]) >= floor_score]

    def mean(rows: list[dict[str, Any]], key: str) -> float:
        return float(np.mean([int(row.get(key, 0)) for row in rows])) if rows else 0.0

    return {
        "byPlaybook": {
            key: _group_summary(rows, floor_score, excellent_score)
            for key, rows in sorted(by_playbook.items())
        },
        "byOpeningTool": {
            key: _group_summary(rows, floor_score, excellent_score)
            for key, rows in sorted(by_opening_tool.items())
        },
        "failureSignals": {
            "failedEpisodes": len(failed),
            "failedOpeningToolMismatch": sum(not bool(row.get("openingToolMatched")) for row in failed),
            "successfulOpeningToolMismatch": sum(not bool(row.get("openingToolMatched")) for row in succeeded),
            "failedMeanGuideDeviations": mean(failed, "guideDeviationCount"),
            "successfulMeanGuideDeviations": mean(succeeded, "guideDeviationCount"),
            "failedMeanFoundationMistakes": mean(failed, "foundationMistakeCount"),
            "successfulMeanFoundationMistakes": mean(succeeded, "foundationMistakeCount"),
        },
    }


def _format_score(value: float | int) -> str:
    return f"{value:,.0f}"


def print_score_summary(summary: dict[str, Any]) -> None:
    print(f"评估 {summary['episodes']} 局（确定性策略、固定未训练评估种子）")
    print(f"平均分：{_format_score(summary['mean'])}")
    print(f"中间 80% 均分：{_format_score(summary['middle80Mean'])}（剔除最高、最低各 10%）")
    print(
        "最低 / P10 / 中位数 / P90 / 最高："
        f"{_format_score(summary['minimum'])} / {_format_score(summary['p10'])} / "
        f"{_format_score(summary['median'])} / {_format_score(summary['p90'])} / "
        f"{_format_score(summary['maximum'])}"
    )
    print(f"方差：{summary['variance']:,.2f}；标准差：{summary['standardDeviation']:,.2f}")
    tiers = summary["tiers"]
    print(
        f"档位：失败 {tiers['failed']} / 保底 {tiers['floor']} / 优秀 {tiers['excellent']}"
        f"（其中达到封顶 {tiers['capped']}）"
    )
    print(
        f"保底率：{tiers['floorOrBetterRate']:.1%}；优秀率：{tiers['excellentRate']:.1%}；"
        f"封顶有效期望：{_format_score(tiers['cappedUsefulMean'])}"
    )
