"""Live terminal dashboard for MaskablePPO training."""

from __future__ import annotations

from collections import deque
from time import perf_counter
from typing import Any

import numpy as np
from rich.console import Group
from rich.live import Live
from rich.panel import Panel
from rich.progress_bar import ProgressBar
from rich.table import Table
from rich.text import Text
from stable_baselines3.common.callbacks import BaseCallback


class TrainingDashboard(BaseCallback):
    """Render training and one representative parallel environment in a TUI."""

    def __init__(
        self,
        total_timesteps: int,
        envs: int,
        device: str,
        *,
        start_timesteps: int = 0,
        refresh_seconds: float = 1.0,
        floor_score: int = 607_000,
        excellent_score: int = 721_000,
        score_cap: int = 1_120_000,
    ):
        super().__init__()
        if refresh_seconds < 0.1:
            raise ValueError("TUI refresh interval must be at least 0.1 seconds")
        self.total_timesteps = total_timesteps
        self.start_timesteps = start_timesteps
        self.envs = envs
        self.device = device
        self.refresh_seconds = refresh_seconds
        self.floor_score = floor_score
        self.excellent_score = excellent_score
        self.score_cap = score_cap
        self.started_at = 0.0
        self.completed_episodes = 0
        self.last_reward = 0.0
        self.last_raw_reward = 0.0
        self.last_objective_reward = 0.0
        self.last_preference_reward = 0.0
        self.last_preference_weight = 0.0
        self.last_playbook = "未匹配攻略流派"
        self.last_observation: dict[str, np.ndarray] | None = None
        self.last_semantic_observation: dict[str, Any] | None = None
        self.last_action: int | None = None
        self.reward_history: deque[float] = deque(maxlen=160)
        self.episode_scores: deque[int] = deque(maxlen=1_000)
        self.metric_history: dict[str, deque[float]] = {
            "train/approx_kl": deque(maxlen=80),
            "train/explained_variance": deque(maxlen=80),
            "train/value_loss": deque(maxlen=80),
        }
        self.last_recorded_update = -1
        self.last_draw_at = 0.0
        self.live = Live(self._render(), auto_refresh=False, screen=True, transient=False)
        self.running = False

    def _on_training_start(self) -> None:
        self.started_at = perf_counter()
        self.running = True
        self.live.start(refresh=True)
        self.last_draw_at = self.started_at

    def _on_step(self) -> bool:
        observation = self.locals.get("new_obs")
        if isinstance(observation, dict):
            self.last_observation = observation
        rewards = self.locals.get("rewards")
        if rewards is not None:
            self.last_reward = float(np.mean(rewards))
        infos = self.locals.get("infos", [])
        if infos and isinstance(infos[0].get("semantic_observation"), dict):
            self.last_semantic_observation = infos[0]["semantic_observation"]
        raw_rewards = [float(info["raw_reward"]) for info in infos if "raw_reward" in info]
        if raw_rewards:
            self.last_raw_reward = float(np.mean(raw_rewards))
            self.reward_history.append(self.last_raw_reward)
        preference_rewards = [float(info["preference_reward"]) for info in infos if "preference_reward" in info]
        if preference_rewards:
            self.last_preference_reward = float(np.mean(preference_rewards))
        objective_rewards = [float(info["objective_reward"]) for info in infos if "objective_reward" in info]
        if objective_rewards:
            self.last_objective_reward = float(np.mean(objective_rewards))
        if infos and "preference_weight" in infos[0]:
            self.last_preference_weight = float(infos[0]["preference_weight"])
        if infos and "playbook" in infos[0]:
            self.last_playbook = str(infos[0]["playbook"])
        dones = self.locals.get("dones")
        if dones is not None:
            self.completed_episodes += int(np.count_nonzero(dones))
            for done, info in zip(dones, infos):
                if done and "score" in info:
                    self.episode_scores.append(int(info["score"]))
            self._record_tier_metrics()
        actions = self.locals.get("actions")
        if actions is not None and len(actions):
            self.last_action = int(actions[0])
        self._record_metrics()
        self._refresh()
        return True

    def _on_rollout_end(self) -> None:
        self._refresh(force=True)

    def _on_rollout_start(self) -> None:
        self._record_metrics()
        self._refresh(force=True)

    def _on_training_end(self) -> None:
        self.stop()

    def stop(self) -> None:
        if self.running:
            self.live.stop()
            self.running = False

    def _refresh(self, *, force: bool = False) -> None:
        now = perf_counter()
        if self.running and (force or now - self.last_draw_at >= self.refresh_seconds):
            self.live.update(self._render(), refresh=True)
            self.last_draw_at = now

    def _render(self) -> Group:
        elapsed = max(perf_counter() - self.started_at, 1e-9)
        completed = min(max(self.num_timesteps - self.start_timesteps, 0), self.total_timesteps)
        rate = completed / elapsed
        remaining = max(self.total_timesteps - completed, 0)
        eta = remaining / rate if rate else 0.0

        summary = Table.grid(expand=True)
        summary.add_column(justify="left")
        summary.add_column(justify="right")
        summary.add_row(
            "本次进度",
            f"{completed:,} / {self.total_timesteps:,} steps  ·  累计 {self.num_timesteps:,}",
        )
        summary.add_row("速度", f"{rate:,.0f} steps/s  ·  ETA {_duration(eta)}")
        summary.add_row("设备 / 并行环境", f"{self.device} / {self.envs}")
        summary.add_row("已完成局数", f"{self.completed_episodes:,}")
        if self.episode_scores:
            scores = np.asarray(self.episode_scores)
            summary.add_row(
                f"近期档位（{len(scores)} 局）",
                f"保底 {np.mean(scores >= self.floor_score):.1%}  ·  优秀 {np.mean(scores >= self.excellent_score):.1%}",
            )
        summary.add_row("本步原始分数", f"{self.last_raw_reward:,.1f}")
        summary.add_row("档位 / PPO 奖励", f"{self.last_objective_reward:+.4f} / {self.last_reward:+.4f}")
        summary.add_row("相对攻略奖励", f"{self.last_preference_reward:+.4f}  (权重 {self.last_preference_weight:.3f})")
        metrics = self.logger.name_to_value if hasattr(self, "model") else {}
        training = _metric_rows(metrics)
        if training:
            summary.add_row("训练", training)

        state = Table.grid(expand=True)
        state.add_column(style="bold cyan")
        state.add_column()
        for label, value in self._state_rows():
            state.add_row(label, value)

        progress = Group(
            ProgressBar(total=self.total_timesteps, completed=completed, pulse=completed < self.total_timesteps),
            Text(f"{completed / self.total_timesteps:.1%}", justify="center"),
        )
        return Group(
            Panel(progress, title="Vorax MaskablePPO", border_style="green"),
            Panel(summary, title="训练", border_style="blue"),
            Panel(self._trend_table(), title="趋势（近期与前一窗口对比）", border_style="yellow"),
            Panel(state, title="环境 0（当前状态）", border_style="magenta"),
        )

    def _record_metrics(self) -> None:
        metrics = self.logger.name_to_value
        update = int(metrics.get("train/n_updates", -1))
        if update < 0 or update == self.last_recorded_update:
            return
        self.last_recorded_update = update
        for name, history in self.metric_history.items():
            if name in metrics:
                history.append(float(metrics[name]))

    def _record_tier_metrics(self) -> None:
        if not self.episode_scores or not hasattr(self, "logger"):
            return
        scores = np.asarray(self.episode_scores, dtype=np.float64)
        useful = np.where(scores < self.floor_score, 0, np.minimum(scores, self.score_cap))
        self.logger.record("rollout/floor_rate", float(np.mean(scores >= self.floor_score)))
        self.logger.record("rollout/excellent_rate", float(np.mean(scores >= self.excellent_score)))
        self.logger.record("rollout/capped_useful_mean", float(np.mean(useful)))

    def _trend_table(self) -> Table:
        table = Table(expand=True)
        table.add_column("指标", style="bold")
        table.add_column("当前", justify="right")
        table.add_column("近期变化", justify="right")
        table.add_column("判断")
        table.add_column("变化折线", min_width=24)
        self._trend_row(table, "原始奖励", self.reward_history, higher_is_better=True)
        self._trend_row(table, "解释方差", self.metric_history["train/explained_variance"], higher_is_better=True)
        self._trend_row(table, "价值损失", self.metric_history["train/value_loss"], higher_is_better=False)
        self._kl_row(table, self.metric_history["train/approx_kl"])
        return table

    def _trend_row(self, table: Table, label: str, history: deque[float], *, higher_is_better: bool) -> None:
        current, delta = _window_change(history)
        if current is None:
            table.add_row(label, "—", "—", "等待更新", "")
            return
        direction = delta > 0 if higher_is_better else delta < 0
        if abs(delta) < max(abs(current) * 0.01, 1e-6):
            verdict = "→ 持平"
        else:
            verdict = "↑ 改善" if direction else "↓ 变弱"
        table.add_row(label, f"{current:.4g}", f"{delta:+.3g}", verdict, _sparkline(history))

    def _kl_row(self, table: Table, history: deque[float]) -> None:
        current, delta = _window_change(history)
        if current is None:
            table.add_row("近似 KL", "—", "—", "等待更新", "")
            return
        verdict = "✓ 稳定" if 0.003 <= current <= 0.03 else "! 过高" if current > 0.03 else "! 过低"
        table.add_row("近似 KL", f"{current:.4g}", f"{delta:+.3g}", verdict, _sparkline(history))

    def _state_rows(self) -> list[tuple[str, str]]:
        observation = self.last_observation
        if observation is None:
            return [("状态", "等待首个环境步…")]
        phase = int(observation["phase"][0][0])
        phase_name = {1: "准备", 2: "选择", 3: "结束"}.get(phase, f"未知 ({phase})")
        progress = observation["progress"][0].tolist()
        score = int(observation["score"][0][0])
        legal_actions = int(np.count_nonzero(observation["action_mask"][0]))
        candidates = observation["candidate_cards"][0]
        playable = observation["candidate_playable"][0]
        semantic_cards = (self.last_semantic_observation or {}).get("cards", [])
        visible_cards = [
            str(card.get("name") or card.get("id"))
            for card in semantic_cards
            if card.get("playable")
        ]
        if not visible_cards:
            visible_cards = [str(int(card)) for card, enabled in zip(candidates, playable, strict=True) if enabled]
        return [
            ("阶段", phase_name),
            ("回合进度", f"{progress[0]} / {progress[1]}"),
            ("当前分数", f"{score:,}"),
            ("锁定流派", self.last_playbook),
            ("合法动作", str(legal_actions)),
            ("本环境最近动作", "—" if self.last_action is None else str(self.last_action)),
            ("可选卡牌", ", ".join(visible_cards) if visible_cards else "—"),
        ]


def _metric_rows(metrics: dict[str, Any]) -> str:
    labels = ("train/approx_kl", "train/entropy_loss", "train/policy_gradient_loss", "train/value_loss", "train/explained_variance")
    values = [f"{name.removeprefix('train/')}: {float(metrics[name]):.4g}" for name in labels if name in metrics]
    return "  ·  ".join(values)


def _duration(seconds: float) -> str:
    seconds = int(seconds)
    minutes, seconds = divmod(seconds, 60)
    hours, minutes = divmod(minutes, 60)
    return f"{hours:d}:{minutes:02d}:{seconds:02d}" if hours else f"{minutes:d}:{seconds:02d}"


def _window_change(history: deque[float]) -> tuple[float | None, float]:
    if not history:
        return None, 0.0
    values = list(history)
    window = min(16, max(1, len(values) // 2))
    current = float(np.mean(values[-window:]))
    previous = float(np.mean(values[-2 * window : -window])) if len(values) >= 2 * window else values[0]
    return current, current - previous


def _sparkline(values: deque[float]) -> str:
    points = list(values)
    if not points:
        return ""
    if len(points) > 32:
        indexes = np.linspace(0, len(points) - 1, 32, dtype=int)
        points = [points[index] for index in indexes]
    low, high = min(points), max(points)
    if high == low:
        return "▅" * len(points)
    glyphs = "▁▂▃▄▅▆▇█"
    return "".join(glyphs[round((value - low) / (high - low) * (len(glyphs) - 1))] for value in points)
