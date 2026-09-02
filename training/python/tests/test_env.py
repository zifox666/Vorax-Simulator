import json
from time import perf_counter
from unittest.mock import Mock

import httpx
import numpy as np
import pytest
from gymnasium.utils.env_checker import check_env

from vorax_gym import VoraxEnv, VoraxVectorEnv
from vorax_gym.evaluate import print_summary, summarize_scores
from vorax_gym.reporting import summarize_diagnostics
from vorax_gym.train import (
    build_sb3_vector_env,
    evaluation_report_path,
    evaluate_vectorized,
    evaluate_vectorized_with_diagnostics,
)


SPEC = {
    "specVersion": "training-v1",
    "monsterIds": ["m1"],
    "cardIds": ["c1"],
    "toolIds": ["t1"],
    "actions": [{"index": 0, "action": {"type": "skip_unknown"}}, {"index": 1, "action": {"type": "choose", "cardId": "c1"}}],
    "tensor": {"toolCount": 1, "actionCount": 2},
}


def transition(token="token", terminated=False):
    return {
        "episodeToken": token,
        "observation": {"phase": "FINISHED" if terminated else "PREPARING"},
        "tensorObservation": {
            "phase": 3 if terminated else 1,
            "progress": [12 if terminated else 0, 11 if terminated else 0],
            "score": "10" if terminated else "0",
            "slotMonsters": [0] * 6,
            "slotFamilies": [0] * 6,
            "slotRarities": [0] * 6,
            "slotActivities": ["0"] * 6,
            "slotQuantities": ["0"] * 6,
            "toolCounts": [0],
            "offer": [0 if terminated else 1],
            "offerRewardThreshold": "0",
            "candidateCards": [0] * 5,
            "candidatePlayable": [0] * 5,
            "refreshes": [3, 0],
            "rewardJars": [0] * 6,
            "dropBonusPercent": 0,
            "toolClaimStatuses": [0, 0],
            "nextRewardThreshold": "0",
        },
        "legalActions": [] if terminated else [{"type": "skip_unknown"}],
        "actionMask": [0, 0] if terminated else [1, 0],
        "reward": "10" if terminated else "0",
        "terminated": terminated,
        "truncated": False,
        "info": {"score": "10" if terminated else "0", "specVersion": "training-v1"},
    }


def transport():
    def handler(request: httpx.Request):
        path = request.url.path
        if path.endswith("/spec"):
            data = SPEC
        elif path.endswith("/batch/reset"):
            count = len(json.loads(request.content)["items"])
            data = {"results": [{"transition": transition(f"r{i}")} for i in range(count)]}
        elif path.endswith("/batch/step"):
            count = len(json.loads(request.content)["items"])
            data = {"results": [{"transition": transition(f"s{i}", True)} for i in range(count)]}
        elif path.endswith("/reset"):
            data = transition("reset")
        elif path.endswith("/step"):
            data = transition("step", True)
        else:
            return httpx.Response(404, json={"code": "NOT_FOUND"})
        return httpx.Response(200, json=data)

    return httpx.MockTransport(handler)


def test_single_env_shapes_and_termination():
    env = VoraxEnv("http://test", "key", transport=transport())
    check_env(env, skip_render_check=True)
    observation, info = env.reset(seed=42)
    assert env.observation_space.contains(observation)
    assert observation["action_mask"].tolist() == [1, 0]
    observation, reward, terminated, truncated, info = env.step(0)
    assert env.observation_space.contains(observation)
    assert (reward, terminated, truncated, info["score"]) == (10.0, True, False, 10)
    env.close()


def test_vector_env_batches_and_requires_explicit_reset():
    env = VoraxVectorEnv("http://test", "key", 3, transport=transport())
    observation, info = env.reset(seed=10)
    assert observation["phase"].shape == (3, 1)
    observation, rewards, terminated, truncated, info = env.step(np.zeros(3, dtype=np.int64))
    assert terminated.tolist() == [True, True, True]
    assert rewards.tolist() == [10.0, 10.0, 10.0]
    observation, info = env.reset(options={"reset_mask": terminated})
    assert observation["action_mask"].shape == (3, 2)
    env.close()


def test_vectorized_evaluation_preserves_episode_count_and_uses_masks():
    class Model:
        def predict(self, observation, *, action_masks, deterministic):
            assert deterministic is True
            assert np.array_equal(action_masks, observation["action_mask"].astype(bool))
            return np.zeros(len(action_masks), dtype=np.int64), None

    env = VoraxVectorEnv("http://test", "key", 3, transport=transport())
    try:
        assert evaluate_vectorized(Model(), env, episodes=5, seed=2026) == [10, 10, 10, 10, 10]
    finally:
        env.close()


def test_detailed_evaluation_reports_refresh_counts():
    class RefreshModel:
        def predict(self, observation, *, action_masks, deterministic):
            observation["offer"][:] = 2
            return np.ones(len(action_masks), dtype=np.int64), None

    env = VoraxVectorEnv("http://test", "key", 2, transport=transport())
    env.specification["actions"][1]["action"] = {"type": "refresh"}
    try:
        scores, refreshes, diagnostics = evaluate_vectorized_with_diagnostics(
            RefreshModel(), env, episodes=2, seed=2026
        )
        assert scores == [10, 10]
        assert refreshes["potion"] == [1, 1]
        assert refreshes["earlyPotion"] == [1, 1]
        assert diagnostics[0]["seed"] == 102026
        assert diagnostics[0]["score"] == 10
        assert diagnostics[0]["actions"][0]["action"] == {"type": "refresh"}
        assert diagnostics[0]["finalSlots"] == []
    finally:
        env.close()


def test_sb3_batch_environment_advertises_action_masking(monkeypatch):
    VecEnv = pytest.importorskip("stable_baselines3.common.vec_env").VecEnv

    class Arguments:
        server = "http://test"
        api_key = "key"
        envs = 3
        pet_refreshes = 0
        seed = 42
        timesteps = 1_000
        reward_mode = "score"
        reward_scale = 10_000
        preference_weight = 0.25

    class MockVectorEnv(VoraxVectorEnv):
        def __init__(self, *args, **kwargs):
            kwargs["transport"] = transport()
            super().__init__(*args, **kwargs)

    monkeypatch.setattr("vorax_gym.train.VoraxVectorEnv", MockVectorEnv)
    env = build_sb3_vector_env(VecEnv, Arguments())
    try:
        assert env.has_attr("action_masks")
        observation = env.reset()
        assert np.array_equal(env.action_masks(), observation["action_mask"].astype(bool))
        with pytest.raises(RuntimeError, match="outside the client action mask"):
            env.step_async(np.ones(3, dtype=np.int64))
        env.step_async(np.zeros(3, dtype=np.int64))
        next_observation, rewards, dones, infos = env.step_wait()
        assert rewards.tolist() == pytest.approx([0.001, 0.001, 0.001])
        assert dones.tolist() == [True, True, True]
        assert all(info["playbook"] == "未匹配攻略流派" for info in infos)
        assert next_observation["action_mask"].shape == (3, 2)
    finally:
        env.close()


def test_mps_policy_samples_only_legal_masked_actions():
    torch = pytest.importorskip("torch")
    if not torch.backends.mps.is_available():
        pytest.skip("MPS is unavailable")

    from vorax_gym.env import _spaces
    from vorax_gym.policy import MPSMaskableMultiInputPolicy

    observation_space, action_space = _spaces(SPEC)
    policy = MPSMaskableMultiInputPolicy(observation_space, action_space, lambda _: 3e-4, net_arch=[16]).to("mps")
    observation = {
        key: torch.zeros((1, *space.shape), dtype=torch.from_numpy(np.empty((), dtype=space.dtype)).dtype, device="mps")
        for key, space in observation_space.spaces.items()
    }
    mask = np.array([[False, True]])
    with torch.no_grad():
        for _ in range(100):
            actions, _, _ = policy(observation, action_masks=mask)
            assert actions.item() == 1


def test_feature_extractor_one_hot_encodes_categories_and_ignores_action_mask():
    torch = pytest.importorskip("torch")
    from vorax_gym.env import _spaces
    from vorax_gym.policy import VoraxFeaturesExtractor

    observation_space, _ = _spaces(SPEC)
    extractor = VoraxFeaturesExtractor(observation_space)
    observation = {
        key: torch.from_numpy(np.zeros((2, *space.shape), dtype=space.dtype))
        for key, space in observation_space.spaces.items()
    }
    observation["score"][:] = 1_120_000
    observation["slot_monsters"][:, 0] = 1
    output = extractor(observation)
    observation["action_mask"][:] = 1
    output_with_different_mask = extractor(observation)

    assert output.shape == (2, extractor.features_dim)
    assert torch.isfinite(output).all()
    assert torch.equal(output, output_with_different_mask)


def test_maskable_policy_uses_vorax_feature_extractor_on_cpu():
    torch = pytest.importorskip("torch")
    from vorax_gym.env import _spaces
    from vorax_gym.policy import MPSMaskableMultiInputPolicy, VoraxFeaturesExtractor

    observation_space, action_space = _spaces(SPEC)
    policy = MPSMaskableMultiInputPolicy(
        observation_space,
        action_space,
        lambda _: 3e-4,
        net_arch=[16],
        features_extractor_class=VoraxFeaturesExtractor,
    )
    observation = {
        key: torch.from_numpy(np.zeros((1, *space.shape), dtype=space.dtype))
        for key, space in observation_space.spaces.items()
    }
    with torch.no_grad():
        actions, values, log_prob = policy(observation, action_masks=np.array([[True, False]]))

    assert actions.item() == 0
    assert values.shape == (1, 1)
    assert log_prob.shape == (1,)


def test_tui_throttles_terminal_redraws():
    pytest.importorskip("rich")
    from vorax_gym.tui import TrainingDashboard

    dashboard = TrainingDashboard(200_000, 128, "mps", refresh_seconds=2)
    dashboard.running = True
    dashboard.last_draw_at = perf_counter()
    dashboard.live = Mock()
    dashboard._refresh()
    dashboard.live.update.assert_not_called()
    dashboard._refresh(force=True)
    dashboard.live.update.assert_called_once()


def test_score_summary_reports_trimmed_mean_and_dispersion():
    summary = summarize_scores(list(range(1, 101)))

    assert summary["episodes"] == 100
    assert summary["minimum"] == 1
    assert summary["maximum"] == 100
    assert summary["mean"] == pytest.approx(50.5)
    assert summary["variance"] == pytest.approx(833.25)
    assert summary["middle80Mean"] == pytest.approx(50.5)


def test_score_summary_reports_business_tiers_and_caps_useless_excess():
    summary = summarize_scores([600_000, 607_000, 720_999, 721_000, 1_120_000, 2_000_000])
    tiers = summary["tiers"]

    assert tiers["failed"] == 1
    assert tiers["floor"] == 2
    assert tiers["excellent"] == 3
    assert tiers["capped"] == 2
    assert tiers["floorOrBetterRate"] == pytest.approx(5 / 6)
    assert tiers["excellentRate"] == pytest.approx(0.5)
    assert tiers["cappedUsefulMean"] == pytest.approx((607_000 + 720_999 + 721_000 + 1_120_000 * 2) / 6)


def test_print_summary_keeps_tier_rates(capsys):
    print_summary(summarize_scores([600_000, 607_000, 721_000]))
    output = capsys.readouterr().out
    assert "保底率" in output
    assert "优秀率" in output


def test_diagnostic_summary_groups_playbooks_and_failure_signals():
    rows = [
        {
            "score": 500_000,
            "initialPlaybook": "骨卫兵",
            "actualOpeningTool": "claw",
            "openingToolMatched": False,
            "guideDeviationCount": 2,
            "foundationMistakeCount": 1,
        },
        {
            "score": 800_000,
            "initialPlaybook": "骨卫兵",
            "actualOpeningTool": "claw",
            "openingToolMatched": True,
            "guideDeviationCount": 0,
            "foundationMistakeCount": 0,
        },
    ]
    summary = summarize_diagnostics(rows)
    assert summary["byPlaybook"]["骨卫兵"]["floorOrBetterRate"] == pytest.approx(0.5)
    assert summary["failureSignals"]["failedOpeningToolMismatch"] == 1


def test_training_report_path_is_automatic_but_overridable():
    assert evaluation_report_path(Path("models/ft4"), 2026) == Path("reports/ft4-seed2026.json")
    assert evaluation_report_path(Path("models/ft4"), 2026, Path("custom/report.json")) == Path(
        "custom/report.json"
    )
