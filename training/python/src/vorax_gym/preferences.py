"""Strategy preference shaping derived from ``training/流派偏好.md``.

The server remains the source of truth for game score and legal actions.  This
module only adds a small, configurable training signal so PPO can discover a
coherent build before it has seen enough rare high-scoring episodes.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any


BONE, FIEND, AWAKENER, INSECT = 1, 2, 3, 4
NORMAL, MAGIC, RARE, BOSS = 1, 2, 3, 4


@dataclass(frozen=True)
class Playbook:
    key: str
    name: str
    opening_tool: str
    core_tools: frozenset[str]
    fallback_tools: frozenset[str] = frozenset()
    setup_potions: frozenset[str] = frozenset()
    buff_potions: frozenset[str] = frozenset()
    core_potions: frozenset[str] = frozenset()
    always_pick: frozenset[str] = frozenset()


INSECT_PUPA = Playbook(
    "insect_pupa",
    "蛊虫·人蛹标本",
    "pupa",
    frozenset({"scraper", "tanned_restraint"}),
    setup_potions=frozenset({"insect_powder", "brood_hormone", "insect_twin_hormone", "eggshell_powder"}),
    buff_potions=frozenset({"pure_leech", "mixed_leech", "sticky_bile", "awakening"}),
    always_pick=frozenset({"insect_boost", "lure"}),
)

AWAKENER_BOSS = Playbook(
    "awakener_boss",
    "觉醒者·单核养 BOSS",
    "pituitary",
    frozenset({"fang", "saw"}),
    fallback_tools=frozenset({"cortex"}),
    core_potions=frozenset({"strong_will_powder", "will_powder", "awakening", "pia_mater"}),
)

AWAKENER_DOUBLE = Playbook(
    "awakener_double",
    "觉醒者·双核养 BOSS",
    "frontal_lobe",
    frozenset({"fang", "rawhide_restraint"}),
    core_potions=frozenset({"awaker_fluid", "awaker_anesthetic", "awakening", "mutagen_powder", "mutation"}),
)

FIEND_GROWTH = Playbook(
    "fiend_growth",
    "异魔·孽生肉芽",
    "growth",
    frozenset({"nettle", "fang", "goat_suture", "marrow"}),
    setup_potions=frozenset({"alien_hormone", "insect_powder", "brood_hormone"}),
    buff_potions=frozenset({
        "awakening", "fiend_anesthetic", "petrified_marrow", "gray_marrow",
        "hollow_marrow", "proliferation_powder", "mutagen_powder",
    }),
    core_potions=frozenset({"fresh_marrow_powder", "fiend_anesthetic"}),
)

FIEND_DOUBLE = Playbook(
    "fiend_double",
    "异魔·双异魔变异",
    "liver",
    frozenset({"nettle", "fang", "saw"}),
    setup_potions=FIEND_GROWTH.setup_potions,
    buff_potions=FIEND_GROWTH.buff_potions,
    core_potions=frozenset({"fresh_marrow_powder", "mutagen_powder", "gray_marrow", "mutation"}),
)

_BONE_SETUP = frozenset({"bone_ointment", "alien_hormone", "targeted_alien_hormone", "will_powder"})
_BONE_BUFF = frozenset({
    "awakening", "cleansing_ointment", "mixed_leech", "sticky_bile", "mutation",
    "awaker_anesthetic", "digestive", "fusion", "petrified_marrow", "gray_marrow", "hollow_marrow",
})

BONE_CLAW = Playbook(
    "bone_claw",
    "骨卫兵·挛缩指爪",
    "claw",
    frozenset({"cortex", "goat_suture"}),
    fallback_tools=frozenset({"saw", "scraper", "eye"}),
    setup_potions=_BONE_SETUP,
    buff_potions=_BONE_BUFF,
)

BONE_METATARSAL = Playbook(
    "bone_metatarsal",
    "骨卫兵·双骨粘连跖骨",
    "metatarsal",
    frozenset({"sinew", "scraper"}),
    fallback_tools=frozenset({"tanned_restraint", "cortex"}),
    setup_potions=_BONE_SETUP,
    buff_potions=_BONE_BUFF,
)

PLAYBOOKS = (
    INSECT_PUPA,
    AWAKENER_BOSS,
    AWAKENER_DOUBLE,
    FIEND_GROWTH,
    FIEND_DOUBLE,
    BONE_CLAW,
    BONE_METATARSAL,
)


def select_playbook(observation: dict[str, Any]) -> Playbook | None:
    """Select one guide build from the initial board and lock it for the run."""
    slots = [slot for slot in observation.get("slots", []) if int(slot.get("quantity", 0)) > 0]
    if not slots:
        return None
    counts = {family: 0 for family in (BONE, FIEND, AWAKENER, INSECT)}
    for slot in slots:
        family = int(slot.get("family", 0))
        if family in counts:
            counts[family] += 1
    total = len(slots)
    normal_count = sum(int(slot.get("rarity", 0)) == NORMAL for slot in slots)
    rare_awakeners = sum(
        int(slot.get("family", 0)) == AWAKENER and int(slot.get("rarity", 0)) in (RARE, BOSS)
        for slot in slots
    )
    other_rares = sum(
        int(slot.get("family", 0)) != AWAKENER and int(slot.get("rarity", 0)) in (RARE, BOSS)
        for slot in slots
    )
    dominant = max(counts, key=counts.get)
    candidates: list[tuple[int, Playbook]] = []

    def add(score: int, playbook: Playbook) -> None:
        candidates.append((score, playbook))

    if counts[INSECT] > 0:
        add(60 + counts[INSECT] * 8 + (35 if dominant == INSECT else 0), INSECT_PUPA)
    if rare_awakeners and total <= 3:
        add(105 + rare_awakeners * 5, AWAKENER_BOSS)
    if counts[AWAKENER] >= 2 or (counts[AWAKENER] >= 1 and other_rares >= 1):
        add(100 + counts[AWAKENER] * 5 + (30 if dominant == AWAKENER else 0), AWAKENER_DOUBLE)
    if counts[FIEND] >= 1 and total >= 4 and normal_count * 2 >= total:
        add(90 + normal_count * 4 + (25 if dominant == FIEND else 0), FIEND_GROWTH)
    if counts[FIEND] >= 2 and total >= 4:
        add(110 + counts[FIEND] * 5 + (25 if dominant == FIEND else 0), FIEND_DOUBLE)
    if counts[BONE] > 0:
        add(65 + counts[BONE] * 5 + (30 if dominant == BONE else 0), BONE_CLAW)
    if counts[BONE] >= 2:
        add(110 + counts[BONE] * 5 + (30 if dominant == BONE else 0), BONE_METATARSAL)

    if not candidates:
        return None
    # PLAYBOOKS order makes ties deterministic across Python versions.
    order = {playbook.key: index for index, playbook in enumerate(PLAYBOOKS)}
    return max(candidates, key=lambda item: (item[0], -order[item[1].key]))[1]


def _card_preference_points(playbook: Playbook, observation: dict[str, Any], action: dict[str, Any]) -> float:
    """Score whether the card itself follows the selected playbook."""
    action_type = action.get("type")
    card_id = str(action.get("cardId", ""))
    offered = {str(card.get("id", "")) for card in observation.get("cards", []) if card.get("playable", True)}
    preferred = preferred_cards(playbook)

    if action_type == "refresh":
        return _refresh_preference_points(playbook, observation, offered)
    if action_type != "choose":
        return 0.0
    if card_id == playbook.opening_tool:
        return 4.0
    if card_id in playbook.always_pick:
        return 3.0
    if card_id in playbook.core_tools:
        return 2.5
    if card_id in playbook.fallback_tools:
        return 1.5
    if card_id in playbook.core_potions:
        return 2.5

    base_cursor = int(observation.get("baseCursor", 0))
    if card_id in playbook.setup_potions:
        return 2.0 if base_cursor <= 4 else 1.0
    if card_id in playbook.buff_potions:
        return 1.0 if base_cursor <= 4 else 2.0
    if not offered.isdisjoint(preferred):
        return -0.5
    return 0.0


def _refresh_preference_points(
    playbook: Playbook,
    observation: dict[str, Any],
    offered: set[str],
) -> float:
    offer_kind = int(observation.get("offer", {}).get("kind", 0))
    base_cursor = int(observation.get("baseCursor", 0))
    if offer_kind == 3:
        remaining = int(observation.get("toolRefreshes", 0))
        if remaining <= 0:
            return 0.0
        if base_cursor == 0:
            return -1.0 if playbook.opening_tool in offered else 3.0
        wanted = playbook.core_tools.union(playbook.fallback_tools)
        return -1.0 if not offered.isdisjoint(wanted) else 1.75 + 0.25 * remaining
    if offer_kind == 2:
        remaining = int(observation.get("potionRefreshes", 0))
        if remaining <= 0:
            return 0.0
        wanted = playbook.core_potions.union(playbook.always_pick)
        wanted = wanted.union(playbook.setup_potions if base_cursor <= 4 else playbook.buff_potions)
        if not offered.isdisjoint(wanted):
            return -1.0
        opportunities = max(1, 9 - base_cursor)
        urgency = min(1.0, remaining / opportunities)
        early_bonus = 0.75 if base_cursor <= 4 else 0.0
        return 0.75 + early_bonus + 1.25 * urgency
    return 0.25 if offered.isdisjoint(preferred_cards(playbook)) else -0.5


def _occupied_slots(observation: dict[str, Any]) -> dict[int, dict[str, Any]]:
    slots: dict[int, dict[str, Any]] = {}
    for fallback_index, slot in enumerate(observation.get("slots", [])):
        if int(slot.get("quantity", 0)) <= 0:
            continue
        slots[int(slot.get("index", fallback_index))] = slot
    return slots


def _number(slot: dict[str, Any], field: str) -> float:
    try:
        return float(slot.get(field, 0))
    except (TypeError, ValueError):
        return 0.0


def _family(slot: dict[str, Any]) -> int:
    return int(slot.get("family", 0))


def _rarity(slot: dict[str, Any]) -> int:
    return int(slot.get("rarity", 0))


def _core_family(playbook: Playbook) -> int:
    if playbook.key.startswith("bone_"):
        return BONE
    if playbook.key.startswith("fiend_"):
        return FIEND
    if playbook.key.startswith("awakener_"):
        return AWAKENER
    return INSECT


def _ratio(value: float, maximum: float) -> float:
    return value / maximum if maximum > 0 else 0.0


def _board_maxima(slots: dict[int, dict[str, Any]]) -> tuple[float, float, float]:
    return (
        max((_number(slot, "activity") for slot in slots.values()), default=1.0),
        max((_number(slot, "quantity") for slot in slots.values()), default=1.0),
        max((_number(slot, "activity") * _number(slot, "quantity") for slot in slots.values()), default=1.0),
    )


def _nearest(slots: dict[int, dict[str, Any]], index: int, direction: int) -> dict[str, Any] | None:
    """Return the nearest occupied slot, matching the handbook's empty-slot skipping rule."""
    candidates = [slot_index for slot_index in slots if (slot_index - index) * direction > 0]
    if not candidates:
        return None
    nearest_index = min(candidates) if direction > 0 else max(candidates)
    return slots[nearest_index]


def _removal_tool_bonus(observation: dict[str, Any], slot: dict[str, Any]) -> float:
    tools = {str(tool) for tool in observation.get("tools", [])}
    bonus = 0.15 if "eye" in tools else 0.0
    if "claw" in tools and _family(slot) != BONE:
        bonus += 0.35
    bone_count = sum(_family(current) == BONE for current in _occupied_slots(observation).values())
    if "metatarsal" in tools and bone_count >= 2:
        bonus += 0.35
    return bonus


def _removal_points(
    playbook: Playbook,
    observation: dict[str, Any],
    slot: dict[str, Any],
    slots: dict[int, dict[str, Any]],
) -> float:
    """Positive means this monster is a good removal victim."""
    _, _, max_value = _board_maxima(slots)
    value_ratio = _ratio(_number(slot, "activity") * _number(slot, "quantity"), max_value)
    score = 1.0 - 1.35 * value_ratio
    if _family(slot) == _core_family(playbook):
        score -= 1.1
    else:
        score += 0.35
    if _rarity(slot) == RARE:
        score -= 0.35
    elif _rarity(slot) == BOSS:
        score -= 0.8
    return score + _removal_tool_bonus(observation, slot)


def _selected_slots(
    observation: dict[str, Any], action: dict[str, Any]
) -> tuple[dict[int, dict[str, Any]], list[tuple[int, dict[str, Any]]]]:
    slots = _occupied_slots(observation)
    selected: list[tuple[int, dict[str, Any]]] = []
    for raw_index in action.get("targetSlots", []):
        index = int(raw_index)
        if index in slots:
            selected.append((index, slots[index]))
    return slots, selected


def _conversion_points(
    playbook: Playbook,
    slot: dict[str, Any],
    target_family: int,
) -> float:
    core = _core_family(playbook)
    score = 0.0
    if target_family == core:
        score += 0.75
    if _family(slot) == core and target_family != core:
        score -= 1.15
    elif _family(slot) != target_family:
        score += 0.25
    rarity = _rarity(slot)
    if rarity == NORMAL:
        score += 0.35
    elif rarity == RARE:
        score -= 0.25
    elif rarity == BOSS:
        score -= 0.65
    return score


def target_preference_points(
    playbook: Playbook, observation: dict[str, Any], action: dict[str, Any]
) -> float:
    """Score potion targets using the handbook's exact effect and removal semantics.

    Values are intentionally small compared with card-choice points.  Their job
    is to teach PPO *where* to apply an already desirable potion, not to make a
    poor off-build potion look like a core pick.
    """
    if action.get("type") != "choose":
        return 0.0
    card_id = str(action.get("cardId", ""))
    slots, selected = _selected_slots(observation, action)
    if not selected:
        return 0.0

    max_activity, max_quantity, max_value = _board_maxima(slots)
    core = _core_family(playbook)

    def activity(slot: dict[str, Any]) -> float:
        return _ratio(_number(slot, "activity"), max_activity)

    def quantity(slot: dict[str, Any]) -> float:
        return _ratio(_number(slot, "quantity"), max_quantity)

    def value(slot: dict[str, Any]) -> float:
        return _ratio(_number(slot, "activity") * _number(slot, "quantity"), max_value)

    if card_id in {"alien_hormone", "targeted_alien_hormone"}:
        score = sum(_removal_points(playbook, observation, slot, slots) for _, slot in selected) / len(selected)
        # Alien hormone adds four monsters, so two removals are safer on a crowded board.
        if card_id == "alien_hormone" and len(slots) >= 5:
            score += 0.3 if len(selected) == 2 else -0.2
        return score

    if card_id in {"will_powder", "strong_will_powder"}:
        _, chosen = selected[0]
        victims = [slot for slot in slots.values() if slot is not chosen and _family(slot) != _family(chosen)]
        collateral = (
            sum(_removal_points(playbook, observation, slot, slots) for slot in victims) / len(victims)
            if victims else 0.5
        )
        removal_weight = 0.9 if card_id == "will_powder" else 1.25
        return 1.15 * activity(chosen) + (0.4 if _family(chosen) == core else 0.0) + removal_weight * collateral

    if card_id == "awakening":
        _, chosen = selected[0]
        rarity_score = {NORMAL: 0.15, MAGIC: 0.55, RARE: 0.9, BOSS: -1.2}.get(_rarity(chosen), 0.0)
        return rarity_score + 0.8 * quantity(chosen) + (0.35 if _family(chosen) == core else 0.0)

    if card_id == "cleansing_ointment":
        index, chosen = selected[0]
        score = 0.45 * quantity(chosen)
        if _family(chosen) == BONE:
            score += 0.75
            right = _nearest(slots, index, 1)
            score += 0.8 if right is None else 0.9 * _removal_points(playbook, observation, right, slots)
        elif _family(chosen) == core:
            score += 0.2
        return score

    if card_id in {"mixed_leech", "pure_leech"}:
        chosen_index, chosen = selected[0]
        if card_id == "mixed_leech":
            matches = [
                slot
                for index, slot in slots.items()
                if index != chosen_index and _rarity(slot) == _rarity(chosen)
            ]
        else:
            matches = [
                slot
                for index, slot in slots.items()
                if index != chosen_index and _family(slot) == _family(chosen)
            ]
        expected_quantity = sum(quantity(slot) for slot in matches) / len(matches) if matches else 0.0
        return 0.75 * quantity(chosen) + 0.55 * expected_quantity + (0.3 if _family(chosen) == core else 0.0)

    if card_id == "sticky_bile":
        index, chosen = selected[0]
        right = _nearest(slots, index, 1)
        score = 0.55 * quantity(chosen) + (0.45 if _family(chosen) == core else 0.0)
        if right is None:
            return score - 0.35
        score += 0.45 * quantity(right)
        if _family(chosen) == core and _family(right) != core:
            score += 0.55
        if _family(right) == core and _family(chosen) != core:
            score -= 0.9
        if _rarity(right) == BOSS and (_family(right), _rarity(right)) != (_family(chosen), _rarity(chosen)):
            score -= 0.55
        return score

    if card_id == "mutation":
        rarity_points = {NORMAL: 0.7, MAGIC: 0.15, RARE: -0.45, BOSS: -0.9}
        mutation_tools = {str(tool) for tool in observation.get("tools", [])}.intersection({"growth", "liver"})
        score = sum(
            rarity_points.get(_rarity(slot), 0.0)
            + (0.25 if _family(slot) != core else -0.15)
            + (0.25 if mutation_tools else 0.0)
            for _, slot in selected
        ) / len(selected)
        return score + (0.25 if len(selected) == 2 else 0.0)

    if card_id == "awaker_anesthetic":
        _, chosen = selected[0]
        return _conversion_points(playbook, chosen, AWAKENER) + (0.35 if _family(chosen) != AWAKENER else -0.2)

    if card_id == "digestive":
        index, chosen = selected[0]
        left = _nearest(slots, index, -1)
        removal = 0.85 if left is None else _removal_points(playbook, observation, left, slots)
        return 1.0 * quantity(chosen) + (0.3 if _family(chosen) == core else 0.0) + removal

    if card_id == "fusion":
        if len(selected) == 1:
            _, chosen = selected[0]
            return -0.25 + (0.55 if core == INSECT else 0.0) - (0.55 if _family(chosen) == core != INSECT else 0.0)
        total_activity = sum(_number(slot, "activity") for _, slot in selected)
        total_quantity = sum(_number(slot, "quantity") for _, slot in selected)
        old_value = sum(_number(slot, "activity") * _number(slot, "quantity") for _, slot in selected)
        immediate_gain = max(0.0, total_activity * total_quantity - old_value)
        board_value = sum(_number(slot, "activity") * _number(slot, "quantity") for slot in slots.values())
        score = min(1.6, immediate_gain / max(board_value, 1.0))
        score += 0.55 if core == INSECT else 0.0
        if core != INSECT:
            score -= 0.5 * sum(_family(slot) == core for _, slot in selected)
        return score

    if card_id in {"petrified_marrow", "hollow_marrow"}:
        _, chosen = selected[0]
        non_fiend = _family(chosen) != FIEND
        score = (1.0 if card_id == "petrified_marrow" and non_fiend else 0.65) * quantity(chosen)
        if core == FIEND and non_fiend:
            score += 0.8 if card_id == "petrified_marrow" else 0.4
        elif core != FIEND and _family(chosen) == core:
            score -= 0.8 if card_id == "petrified_marrow" else 0.4
        return score

    if card_id == "pia_mater":
        _, chosen = selected[0]
        multiplier = 1
        if _family(chosen) == AWAKENER and _rarity(chosen) == MAGIC:
            multiplier = 2
        elif _family(chosen) == AWAKENER and _rarity(chosen) in (RARE, BOSS):
            multiplier = 3
        return multiplier * quantity(chosen) + (0.25 if _family(chosen) == core else 0.0)

    if card_id == "mutagen_powder":
        rarity_points = {NORMAL: 0.65, MAGIC: 0.35, RARE: -0.15, BOSS: -0.75}
        score = sum(
            0.9 * activity(slot)
            + rarity_points.get(_rarity(slot), 0.0)
            + (0.2 if _family(slot) == core else 0.0)
            for _, slot in selected
        ) / len(selected)
        return score + (0.2 if len(selected) == 2 else 0.0)

    if card_id == "fiend_anesthetic":
        _, chosen = selected[0]
        return _conversion_points(playbook, chosen, FIEND) + (0.35 if _family(chosen) != FIEND else -0.15)

    if card_id == "bone_twin_hormone":
        _, chosen = selected[0]
        return _conversion_points(playbook, chosen, BONE) + 0.9 * activity(chosen)

    if card_id == "insect_twin_hormone":
        _, chosen = selected[0]
        return _conversion_points(playbook, chosen, INSECT) + 0.25 * value(chosen)

    return 0.0


def preference_points(playbook: Playbook, observation: dict[str, Any], action: dict[str, Any]) -> float:
    """Return card-choice points plus effect-aware potion target points."""
    return _card_preference_points(playbook, observation, action) + target_preference_points(
        playbook, observation, action
    )


def foundation_action_points(
    playbook: Playbook,
    observation: dict[str, Any],
    action: dict[str, Any],
) -> float:
    """Direct shaping for the few opening decisions that establish a build.

    Unlike the general centered guide reward, this signal is deliberately
    strong and sparse: lock the selected opening tool, spend pet refreshes
    while it is absent, and spend early potion refreshes only when no setup
    card is available.
    """
    action_type = str(action.get("type", ""))
    card_id = str(action.get("cardId", ""))
    offered = {str(card.get("id", "")) for card in observation.get("cards", []) if card.get("playable", True)}
    offer = observation.get("offer", {})
    offer_kind = int(offer.get("kind", 0))

    if offer_kind == 3 and int(offer.get("rewardThreshold", 0)) == 0:
        if playbook.opening_tool in offered:
            if action_type == "choose" and card_id == playbook.opening_tool:
                return 1.0
            return -1.0 if action_type == "refresh" else -0.5
        if int(observation.get("toolRefreshes", 0)) > 0:
            return 1.0 if action_type == "refresh" else -0.5
        return 0.0

    base_cursor = int(observation.get("baseCursor", 0))
    if offer_kind == 2 and base_cursor <= 4 and int(observation.get("potionRefreshes", 0)) > 0:
        wanted = playbook.core_potions.union(playbook.always_pick, playbook.setup_potions)
        if offered.isdisjoint(wanted):
            return 1.0 if action_type == "refresh" else -0.5
        return -1.0 if action_type == "refresh" else 0.0
    return 0.0


def preference_advantage(
    playbook: Playbook,
    observation: dict[str, Any],
    action: dict[str, Any],
    legal_actions: list[dict[str, Any]],
) -> float:
    """Return a centered, bounded guide advantage among currently legal actions.

    Absolute guide points are deliberately not paid as reward: doing so grants
    a positive reward on nearly every turn and can outweigh the terminal score
    tiers.  Centering by the current legal choices teaches ranking while making
    states with only one choice, or several equally good choices, reward-neutral.
    """
    if len(legal_actions) < 2:
        return 0.0
    scores = [preference_points(playbook, observation, candidate) for candidate in legal_actions]
    span = max(scores) - min(scores)
    if span <= 1e-9:
        return 0.0
    chosen = preference_points(playbook, observation, action)
    centered = (chosen - sum(scores) / len(scores)) / span
    return max(-1.0, min(1.0, centered))


def preferred_cards(playbook: Playbook) -> frozenset[str]:
    return frozenset({playbook.opening_tool}).union(
        playbook.core_tools,
        playbook.fallback_tools,
        playbook.setup_potions,
        playbook.buff_potions,
        playbook.core_potions,
        playbook.always_pick,
    )


class PreferenceTracker:
    """Episode-local playbook selection and reward calculation."""

    def __init__(self) -> None:
        self.playbook: Playbook | None = None

    def score(self, observation: dict[str, Any], action: dict[str, Any]) -> float:
        self._select(observation)
        if self.playbook is None:
            return 0.0
        return preference_points(self.playbook, observation, action)

    def advantage(
        self,
        observation: dict[str, Any],
        action: dict[str, Any],
        legal_actions: list[dict[str, Any]],
    ) -> float:
        """Score the chosen action relative to all legal actions in this state."""
        self._select(observation)
        if self.playbook is None:
            return 0.0
        return preference_advantage(self.playbook, observation, action, legal_actions)

    def foundation(self, observation: dict[str, Any], action: dict[str, Any]) -> float:
        """Return the sparse opening/early-refresh shaping signal."""
        self._select(observation)
        if self.playbook is None:
            return 0.0
        return foundation_action_points(self.playbook, observation, action)

    def _select(self, observation: dict[str, Any]) -> None:
        if self.playbook is None and observation.get("stageLabel") == "开局手术用具":
            self.playbook = select_playbook(observation)

    @property
    def name(self) -> str:
        return self.playbook.name if self.playbook else "未匹配攻略流派"
