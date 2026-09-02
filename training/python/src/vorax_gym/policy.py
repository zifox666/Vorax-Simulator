"""MaskablePPO policy adaptations for Apple MPS."""

from __future__ import annotations

import numpy as np
import torch as th
from gymnasium import spaces
from sb3_contrib.common.maskable.distributions import MaskableCategoricalDistribution
from sb3_contrib.common.maskable.policies import MaskableMultiInputActorCriticPolicy
from stable_baselines3.common.torch_layers import BaseFeaturesExtractor


class VoraxFeaturesExtractor(BaseFeaturesExtractor):
    """Encode categorical observations correctly and normalize large counters."""

    categorical = (
        "phase",
        "slot_monsters",
        "slot_families",
        "slot_rarities",
        "offer",
        "candidate_cards",
    )
    bounded = {
        "progress": 13.0,
        "tool_counts": 3.0,
        "candidate_playable": 1.0,
        "refreshes": 3.0,
        "reward_jars": 4.0,
        "drop_bonus_percent": 100.0,
        "tool_claim_statuses": 2.0,
    }
    logarithmic = (
        "slot_activities",
        "slot_quantities",
        "offer_reward_threshold",
        "next_reward_threshold",
    )

    def __init__(self, observation_space: spaces.Dict, score_cap: int = 1_120_000):
        if score_cap <= 0:
            raise ValueError("score_cap must be positive")
        self.category_sizes = {
            key: int(np.max(observation_space.spaces[key].high)) + 1
            for key in self.categorical
        }
        category_features = sum(
            int(np.prod(observation_space.spaces[key].shape)) * self.category_sizes[key]
            for key in self.categorical
        )
        numeric_keys = (*self.bounded, *self.logarithmic, "score")
        numeric_features = sum(int(np.prod(observation_space.spaces[key].shape)) for key in numeric_keys)
        super().__init__(observation_space, category_features + numeric_features)
        self.score_cap = float(score_cap)
        self.log_scale = float(np.log1p(score_cap))

    def forward(self, observations: dict[str, th.Tensor]) -> th.Tensor:
        features: list[th.Tensor] = []
        for key in self.categorical:
            values = observations[key].long().clamp(0, self.category_sizes[key] - 1)
            encoded = th.nn.functional.one_hot(values, num_classes=self.category_sizes[key]).float()
            features.append(encoded.flatten(start_dim=1))
        for key, maximum in self.bounded.items():
            features.append((observations[key].float() / maximum).flatten(start_dim=1))
        for key in self.logarithmic:
            values = th.clamp(observations[key].float(), min=0, max=self.score_cap)
            features.append((th.log1p(values) / self.log_scale).flatten(start_dim=1))
        score = th.clamp(observations["score"].float(), min=0, max=self.score_cap) / self.score_cap
        features.append(score.flatten(start_dim=1))
        # action_mask is deliberately excluded: MaskablePPO applies it to the
        # logits directly, so feeding thousands of mask bits into the MLP adds
        # noise and duplicates the legality mechanism.
        return th.cat(features, dim=1)


class MPSMaskableMultiInputPolicy(MaskableMultiInputActorCriticPolicy):
    """Keep PPO on MPS while sampling masked discrete actions on CPU.

    Some MPS builds can sample an action whose masked logit is excluded.  The
    policy/value networks and gradients remain on MPS; only the categorical
    draw is performed by the CPU implementation, which honors zero-probability
    outcomes.
    """

    def forward(
        self,
        obs: th.Tensor,
        deterministic: bool = False,
        action_masks: np.ndarray | None = None,
    ) -> tuple[th.Tensor, th.Tensor, th.Tensor]:
        features = self.extract_features(obs)
        if self.share_features_extractor:
            latent_pi, latent_vf = self.mlp_extractor(features)
        else:
            pi_features, vf_features = features
            latent_pi = self.mlp_extractor.forward_actor(pi_features)
            latent_vf = self.mlp_extractor.forward_critic(vf_features)
        values = self.value_net(latent_vf)
        distribution = self._get_action_dist_from_latent(latent_pi)
        if action_masks is not None:
            distribution.apply_masking(action_masks)
        actions = self._masked_actions(distribution, deterministic)
        log_prob = distribution.log_prob(actions)
        actions = actions.reshape((-1, *self.action_space.shape))
        return actions, values, log_prob

    def _predict(
        self,
        observation: dict[str, th.Tensor],
        deterministic: bool = False,
        action_masks: np.ndarray | None = None,
    ) -> th.Tensor:
        distribution = self.get_distribution(observation, action_masks)
        return self._masked_actions(distribution, deterministic)

    def _masked_actions(self, distribution, deterministic: bool) -> th.Tensor:
        if deterministic or self.device.type != "mps" or not isinstance(distribution, MaskableCategoricalDistribution):
            return distribution.get_actions(deterministic=deterministic)
        cpu_distribution = th.distributions.Categorical(logits=distribution.distribution.logits.detach().cpu())
        return cpu_distribution.sample().to(self.device)
