import json

import httpx
import numpy as np
from gymnasium.utils.env_checker import check_env

from vorax_gym import VoraxEnv, VoraxVectorEnv


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
