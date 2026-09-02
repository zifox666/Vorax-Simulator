import numpy as np

from vorax_gym.guide_evaluate import greedy_guide_actions
from vorax_gym.preferences import PreferenceTracker


def test_greedy_guide_chooses_best_card_and_target_for_each_lane():
    specification = {
        "actions": [
            {"index": 0, "action": {"type": "refresh"}},
            {"index": 1, "action": {"type": "choose", "cardId": "insect_powder"}},
            {
                "index": 2,
                "action": {"type": "choose", "cardId": "targeted_alien_hormone", "targetSlots": [0]},
            },
            {
                "index": 3,
                "action": {"type": "choose", "cardId": "targeted_alien_hormone", "targetSlots": [1]},
            },
        ]
    }
    insect_observation = {
        "stageLabel": "开局手术用具",
        "baseCursor": 1,
        "slots": [{"index": 0, "family": 4, "rarity": 1, "activity": 20, "quantity": 100}],
        "cards": [{"id": "insect_powder", "playable": True}],
    }
    bone_observation = {
        "stageLabel": "开局手术用具",
        "baseCursor": 1,
        "slots": [
            {"index": 0, "family": 1, "rarity": 4, "activity": 120, "quantity": 300},
            {"index": 1, "family": 2, "rarity": 1, "activity": 5, "quantity": 5},
        ],
        "cards": [{"id": "targeted_alien_hormone", "playable": True}],
    }
    observations = {"action_mask": np.asarray([[1, 1, 0, 0], [0, 0, 1, 1]], dtype=np.int8)}
    infos = {"semantic_observation": np.asarray([insect_observation, bone_observation], dtype=object)}

    actions = greedy_guide_actions(
        [PreferenceTracker(), PreferenceTracker()],
        observations,
        infos,
        specification,
    )

    assert actions.tolist() == [1, 3]
