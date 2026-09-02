import pytest

from vorax_gym.rewards import TierRewardConfig, preference_weight_at


def test_tier_utility_jumps_at_business_thresholds_and_caps_score():
    reward = TierRewardConfig(607_000, 721_000, 1_120_000)

    assert reward.utility(0) == 0
    assert reward.utility(606_999) < 1.0
    assert reward.utility(607_000) == pytest.approx(7.0)
    assert reward.utility(720_999) < 8.0
    assert reward.utility(721_000) == pytest.approx(10.0)
    assert reward.utility(1_120_000) == pytest.approx(11.0)
    assert reward.utility(1_500_000) == reward.utility(1_120_000)


def test_transition_rewards_telescope_to_final_utility():
    reward = TierRewardConfig()
    scores = [0, 300_000, 607_000, 800_000, 1_300_000]
    transitions = sum(reward.transition_reward(before, after) for before, after in zip(scores, scores[1:]))

    assert transitions == pytest.approx(reward.utility(scores[-1]))


def test_tier_bonuses_are_configurable_and_floor_first():
    reward = TierRewardConfig(floor_bonus=8.0, excellent_bonus=1.0)

    assert reward.utility(607_000) == pytest.approx(9.0)
    assert reward.utility(721_000) == pytest.approx(11.0)


def test_preference_weight_decays_then_stays_at_final_value():
    assert preference_weight_at(0.2, 0.05, 0, 1_000_000, 0.6) == pytest.approx(0.2)
    assert preference_weight_at(0.2, 0.05, 300_000, 1_000_000, 0.6) == pytest.approx(0.125)
    assert preference_weight_at(0.2, 0.05, 600_000, 1_000_000, 0.6) == pytest.approx(0.05)
    assert preference_weight_at(0.2, 0.05, 900_000, 1_000_000, 0.6) == pytest.approx(0.05)


def test_invalid_tiers_are_rejected():
    with pytest.raises(ValueError):
        TierRewardConfig(721_000, 607_000, 1_120_000)


def test_invalid_tier_bonuses_are_rejected():
    with pytest.raises(ValueError):
        TierRewardConfig(floor_bonus=0)
    with pytest.raises(ValueError):
        TierRewardConfig(excellent_bonus=-1)
