"""Training objectives aligned with the useful in-game score tiers."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class TierRewardConfig:
    """Piecewise utility prioritizing reliable floor clears before excellence."""

    floor_score: int = 607_000
    excellent_score: int = 721_000
    score_cap: int = 1_120_000
    floor_bonus: float = 6.0
    excellent_bonus: float = 2.0

    def __post_init__(self) -> None:
        if self.floor_score <= 0 or not self.floor_score < self.excellent_score < self.score_cap:
            raise ValueError("score tiers must satisfy 0 < floor < excellent < cap")
        if self.floor_bonus <= 0 or self.excellent_bonus < 0:
            raise ValueError("floor bonus must be positive and excellent bonus cannot be negative")

    def utility(self, score: int | float) -> float:
        """Map a score to PPO utility; additional score above the cap is worthless."""
        value = max(float(score), 0.0)
        # Failed runs are the majority early in training. Give them enough
        # continuous signal to improve, while keeping threshold jumps dominant.
        low_progress = min(value, self.floor_score) / self.floor_score
        if value < self.floor_score:
            return low_progress

        floor_progress = min(value - self.floor_score, self.excellent_score - self.floor_score) / (
            self.excellent_score - self.floor_score
        )
        result = low_progress + self.floor_bonus + floor_progress
        if value < self.excellent_score:
            return result

        excellent_progress = min(value - self.excellent_score, self.score_cap - self.excellent_score) / (
            self.score_cap - self.excellent_score
        )
        return result + self.excellent_bonus + excellent_progress

    def transition_reward(self, previous_score: int | float, next_score: int | float) -> float:
        """Potential difference; episode reward telescopes to final tier utility."""
        return self.utility(next_score) - self.utility(previous_score)


def preference_weight_at(
    initial: float,
    final: float,
    elapsed_timesteps: int,
    total_timesteps: int,
    decay_fraction: float,
) -> float:
    """Linearly decay guide shaping, then hold the final weight."""
    if initial < 0 or final < 0 or final > initial:
        raise ValueError("preference weights must satisfy 0 <= final <= initial")
    if total_timesteps < 1 or not 0 < decay_fraction <= 1:
        raise ValueError("total_timesteps must be positive and decay_fraction must be in (0, 1]")
    decay_timesteps = max(total_timesteps * decay_fraction, 1.0)
    progress = min(max(elapsed_timesteps / decay_timesteps, 0.0), 1.0)
    return initial + (final - initial) * progress
