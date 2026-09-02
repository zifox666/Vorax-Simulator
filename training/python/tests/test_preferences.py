from vorax_gym.preferences import (
    AWAKENER_BOSS,
    AWAKENER_DOUBLE,
    BONE_METATARSAL,
    FIEND_DOUBLE,
    INSECT_PUPA,
    PreferenceTracker,
    foundation_action_points,
    preference_advantage,
    preference_points,
    select_playbook,
    target_preference_points,
)


def observation(slots, cards=(), *, cursor=1, stage="开局手术用具"):
    return {
        "stageLabel": stage,
        "baseCursor": cursor,
        "slots": [
            {"family": family, "rarity": rarity, "quantity": "100"}
            for family, rarity in slots
        ],
        "cards": [{"id": card, "playable": True} for card in cards],
    }


def board(*slots, tools=()):
    """Build a six-slot observation; each slot is index/family/rarity/activity/quantity."""
    occupied = {
        index: {
            "index": index,
            "family": family,
            "rarity": rarity,
            "activity": str(activity),
            "quantity": str(quantity),
        }
        for index, family, rarity, activity, quantity in slots
    }
    return {
        "slots": [
            occupied.get(index, {"index": index, "family": 0, "rarity": 0, "activity": "0", "quantity": "0"})
            for index in range(6)
        ],
        "tools": list(tools),
        "cards": [],
        "baseCursor": 5,
    }


def target_score(playbook, state, card_id, *target_slots):
    return target_preference_points(
        playbook,
        state,
        {"type": "choose", "cardId": card_id, "targetSlots": list(target_slots)},
    )


def test_selects_specific_builds_from_initial_family_and_rarity():
    assert select_playbook(observation([(4, 1), (4, 2), (1, 1)])) is INSECT_PUPA
    assert select_playbook(observation([(3, 3), (1, 1)])) is AWAKENER_BOSS
    assert select_playbook(observation([(3, 1), (1, 3), (2, 1), (4, 1)])) is AWAKENER_DOUBLE
    assert select_playbook(observation([(2, 1), (2, 2), (1, 1), (4, 1)])) is FIEND_DOUBLE
    assert select_playbook(observation([(1, 1), (1, 2), (2, 1), (4, 1)])) is BONE_METATARSAL


def test_opening_offer_does_not_change_initial_playbook():
    slots = [(3, 3), (3, 1)]
    assert select_playbook(observation(slots, ["claw"])) is select_playbook(
        observation(slots, ["frontal_lobe"])
    )


def test_setup_then_buff_order_and_always_pick_priority():
    early = observation([(4, 1)], ["insect_powder"], cursor=2)
    late = observation([(4, 1)], ["awakening"], cursor=7)
    assert preference_points(INSECT_PUPA, early, {"type": "choose", "cardId": "insect_powder"}) == 2.0
    assert preference_points(INSECT_PUPA, late, {"type": "choose", "cardId": "awakening"}) == 2.0
    assert preference_points(INSECT_PUPA, late, {"type": "choose", "cardId": "lure"}) == 3.0


def test_refresh_is_encouraged_only_when_offer_has_no_guide_card():
    bad_offer = observation([(3, 3)], ["bone_ointment"])
    bad_offer.update({"offer": {"kind": 3}, "toolRefreshes": 2, "baseCursor": 0})
    good_offer = observation([(3, 3)], ["pituitary"])
    good_offer.update({"offer": {"kind": 3}, "toolRefreshes": 2, "baseCursor": 0})
    assert preference_points(AWAKENER_BOSS, bad_offer, {"type": "refresh"}) > 0
    assert preference_points(AWAKENER_BOSS, good_offer, {"type": "refresh"}) < 0


def test_early_potion_refresh_builds_foundation_but_keeps_setup_card():
    miss = observation([(4, 1)], ["bone_ointment"], cursor=2, stage="药剂选择 2 / 8")
    miss.update({"offer": {"kind": 2}, "potionRefreshes": 3})
    hit = observation([(4, 1)], ["insect_powder"], cursor=2, stage="药剂选择 2 / 8")
    hit.update({"offer": {"kind": 2}, "potionRefreshes": 3})

    assert preference_points(INSECT_PUPA, miss, {"type": "refresh"}) > 1
    assert preference_points(INSECT_PUPA, hit, {"type": "refresh"}) < 0


def test_foundation_signal_forces_opening_lock_and_early_refresh():
    opening_miss = observation([(4, 1)], ["claw"], cursor=0)
    opening_miss.update({"offer": {"kind": 3, "rewardThreshold": 0}, "toolRefreshes": 2})
    assert foundation_action_points(INSECT_PUPA, opening_miss, {"type": "refresh"}) == 1.0
    assert foundation_action_points(INSECT_PUPA, opening_miss, {"type": "choose", "cardId": "claw"}) < 0

    opening_hit = observation([(4, 1)], ["pupa"], cursor=0)
    opening_hit.update({"offer": {"kind": 3, "rewardThreshold": 0}, "toolRefreshes": 2})
    assert foundation_action_points(INSECT_PUPA, opening_hit, {"type": "choose", "cardId": "pupa"}) == 1.0
    assert foundation_action_points(INSECT_PUPA, opening_hit, {"type": "refresh"}) == -1.0

    potion_miss = observation([(4, 1)], ["bone_ointment"], cursor=2, stage="药剂选择 2 / 8")
    potion_miss.update({"offer": {"kind": 2}, "potionRefreshes": 3})
    assert foundation_action_points(INSECT_PUPA, potion_miss, {"type": "refresh"}) == 1.0


def test_tracker_locks_playbook_for_the_episode():
    tracker = PreferenceTracker()
    opening = observation([(3, 3), (1, 1)], ["pituitary"])
    assert tracker.score(opening, {"type": "choose", "cardId": "pituitary"}) == 4.0
    assert tracker.playbook is AWAKENER_BOSS

    changed_board = observation([(1, 1), (1, 1), (2, 1), (4, 1)], ["fang"], cursor=5, stage="药剂选择 5 / 8")
    tracker.score(changed_board, {"type": "choose", "cardId": "fang"})
    assert tracker.playbook is AWAKENER_BOSS


def test_direct_removal_prefers_weak_non_core_and_protects_boss_core():
    state = board(
        (0, 1, 4, 120, 300),
        (1, 2, 1, 10, 15),
        (2, 4, 1, 25, 40),
        tools=("claw",),
    )
    weak_non_core = target_score(BONE_METATARSAL, state, "targeted_alien_hormone", 1)
    boss_core = target_score(BONE_METATARSAL, state, "targeted_alien_hormone", 0)
    assert weak_non_core > boss_core + 2.0


def test_cleansing_uses_nearest_nonempty_right_slot_for_removal_cost():
    state = board(
        (0, 1, 1, 40, 100),
        (2, 2, 1, 5, 5),
        (3, 1, 1, 40, 100),
        (5, 1, 4, 180, 300),
    )
    # Slot 1 is empty: slot 0 therefore removes weak slot 2. Slot 3 would
    # remove the valuable BOSS in slot 5.
    safe = target_score(BONE_METATARSAL, state, "cleansing_ointment", 0)
    destructive = target_score(BONE_METATARSAL, state, "cleansing_ointment", 3)
    assert safe > destructive + 1.0


def test_digestive_rewards_high_quantity_but_charges_for_left_neighbor():
    state = board(
        (0, 2, 1, 5, 5),
        (2, 1, 1, 40, 200),
        (3, 1, 4, 180, 300),
        (5, 1, 1, 40, 200),
    )
    safe = target_score(BONE_METATARSAL, state, "digestive", 2)
    removes_boss = target_score(BONE_METATARSAL, state, "digestive", 5)
    assert safe > removes_boss


def test_will_powder_values_activity_and_random_removal_pool():
    state = board(
        (0, 1, 3, 160, 100),
        (1, 1, 1, 20, 100),
        (2, 2, 1, 5, 5),
        (3, 4, 1, 6, 6),
    )
    high_activity_core = target_score(BONE_METATARSAL, state, "will_powder", 0)
    low_activity_non_core = target_score(BONE_METATARSAL, state, "will_powder", 2)
    assert high_activity_core > low_activity_non_core


def test_pia_mater_prioritizes_high_rarity_awakener():
    state = board(
        (0, 3, 3, 30, 100),
        (1, 3, 1, 30, 100),
        (2, 1, 4, 30, 100),
    )
    rare_awakener = target_score(AWAKENER_BOSS, state, "pia_mater", 0)
    normal_awakener = target_score(AWAKENER_BOSS, state, "pia_mater", 1)
    non_awakener_boss = target_score(AWAKENER_BOSS, state, "pia_mater", 2)
    assert rare_awakener > normal_awakener
    assert rare_awakener > non_awakener_boss


def test_mutagen_prefers_high_activity_low_rarity_and_two_targets():
    state = board(
        (0, 3, 1, 150, 50),
        (1, 2, 1, 120, 50),
        (2, 3, 4, 160, 50),
    )
    two_good = target_score(AWAKENER_DOUBLE, state, "mutagen_powder", 0, 1)
    boss = target_score(AWAKENER_DOUBLE, state, "mutagen_powder", 2)
    assert two_good > boss


def test_fusion_scores_cross_term_gain_and_penalizes_single_target():
    state = board(
        (0, 4, 1, 180, 10),
        (1, 4, 1, 10, 180),
        (2, 2, 1, 20, 20),
    )
    complementary_pair = target_score(INSECT_PUPA, state, "fusion", 0, 1)
    single = target_score(INSECT_PUPA, state, "fusion", 0)
    assert complementary_pair > single + 1.0


def test_family_conversion_protects_other_build_core_monsters():
    state = board(
        (0, 1, 4, 150, 200),
        (1, 2, 1, 20, 20),
    )
    convert_filler = target_score(BONE_METATARSAL, state, "fiend_anesthetic", 1)
    overwrite_bone_boss = target_score(BONE_METATARSAL, state, "fiend_anesthetic", 0)
    assert convert_filler > overwrite_bone_boss + 1.0


def test_guide_reward_is_relative_to_legal_actions_not_always_positive():
    state = observation([(4, 1)], ["insect_powder", "bone_ointment"], cursor=2)
    best = {"type": "choose", "cardId": "insect_powder"}
    poor = {"type": "choose", "cardId": "bone_ointment"}
    legal = [best, poor, {"type": "refresh"}]

    assert preference_advantage(INSECT_PUPA, state, best, legal) > 0
    assert preference_advantage(INSECT_PUPA, state, poor, legal) < 0
    assert preference_advantage(INSECT_PUPA, state, best, [best]) == 0


def test_relative_target_reward_ranks_safe_removal_above_core_removal():
    state = board(
        (0, 1, 4, 120, 300),
        (1, 2, 1, 10, 15),
    )
    safe = {"type": "choose", "cardId": "targeted_alien_hormone", "targetSlots": [1]}
    destructive = {"type": "choose", "cardId": "targeted_alien_hormone", "targetSlots": [0]}
    legal = [safe, destructive]

    assert preference_advantage(BONE_METATARSAL, state, safe, legal) > 0
    assert preference_advantage(BONE_METATARSAL, state, destructive, legal) < 0
