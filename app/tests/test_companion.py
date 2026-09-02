import asyncio
from copy import deepcopy
from dataclasses import replace
import json
from pathlib import Path

import httpx
import pytest

from vorax_assistant.controller import Controller
from vorax_assistant.client import Client, ServiceError
from vorax_assistant.ocr import Frame
from vorax_assistant.parser import Parser, ReadError, match_name, normalized
from vorax_assistant.session import Session
from vorax_assistant.tui import GUIDANCE, action_text


FIXTURES = Path(__file__).parent / "fixtures"
CATALOG = json.loads((FIXTURES / "catalog.json").read_text(encoding="utf-8"))


def frame(number):
    return Frame.from_dict(json.loads((FIXTURES / f"{number}.json").read_text(encoding="utf-8")))


def sample(number):
    return Parser(CATALOG).parse(frame(number))


def choose(session, card_id):
    session.suggestion = {"type": "choose", "cardId": card_id, "targetSlots": []}
    return session


def test_ocr_geometry_empty_slots_and_text_boundary():
    parser = Parser(CATALOG)
    opening, potion, reward, scheme = [sample(n) for n in (2, 3, 6, 17)]
    assert opening.score == 588 and opening.card_ids == ["pituitary", "claw", "growth"]
    assert potion.refreshes == 3 and potion.round == 1
    assert reward.slots[0]["definitionId"] == "" and reward.slots[1]["index"] == 1
    assert scheme.slots[5]["definitionId"] == "" and scheme.score == 236565
    assert scheme.card_labels["scheme_0"] == "全身针灸疗法"
    f = frame(6)
    scaled = Frame(f.width * 2, f.height * 2 + 40,
                   [replace(line, box=[[x * 2, y * 2 + 40] for x, y in line.box]) for line in f.lines])
    assert parser.parse(scaled).slots == reward.slots
    activity_icon = replace(frame(3), lines=[
        replace(line, text="$36") if line.text == "36" else line for line in frame(3).lines
    ])
    assert parser.parse(activity_icon).slots == potion.slots
    assert normalized("麻药酊剂 一 觉醒者") == normalized("麻药酊剂-觉醒者")
    assert match_name("孪生激素-骨卫兵", CATALOG["cards"]) is None
    assert match_name("异端学者", CATALOG["monsters"])["id"] == "awakener_heretic_scholar"
    broken = replace(f, lines=[line for line in f.lines if line.text != "异端学者"])
    with pytest.raises(ReadError, match="不能判为空槽"):
        parser.parse(broken)
    broken = replace(f, lines=[replace(line, text="16600") if line.text == "16599" else line for line in f.lines])
    with pytest.raises(ReadError, match="不一致"):
        parser.parse(broken)


def test_ocr_missing_card_error_includes_names_and_raw_text():
    f = frame(3)
    broken = replace(f, lines=[replace(line, text="复方焕生丸剂") if line.text == "纯粹活蛭溶液" else line for line in f.lines])
    with pytest.raises(ReadError) as caught:
        Parser(CATALOG).parse(broken)
    message = str(caught.value)
    assert "匹配到 2 张" in message
    assert "靶向异种激素" in message and "空心脊髓溶液" in message
    assert "未匹配 OCR 原文：「复方焕生丸剂」" in message

    broken = replace(f, lines=[replace(line, text="纯粹活蛭溶液") if line.text == "靶向异种激素" else line for line in f.lines])
    with pytest.raises(ReadError, match="重复匹配：纯粹活蛭溶液"):
        Parser(CATALOG).parse(broken)

    known = {"纯粹活蛭溶液", "靶向异种激素", "空心脊髓溶液"}
    broken = replace(f, lines=[replace(line, text="未知药剂甲") if line.text in known else line for line in f.lines])
    with pytest.raises(ReadError) as caught:
        Parser(CATALOG).parse(broken)
    assert "匹配到 0 张" in str(caught.value) and "未知药剂甲" in str(caught.value)


def test_ocr_missing_labels_slots_and_score_are_specific():
    f = frame(3)
    for label in ("最终活性", "下档奖励"):
        broken = replace(f, lines=[line for line in f.lines if line.text != label])
        with pytest.raises(ReadError, match=f"缺少文字：{label}；"):
            Parser(CATALOG).parse(broken)
    broken = replace(f, lines=[replace(line, text="未知怪物") if line.text == "异端学者" else line for line in f.lines])
    with pytest.raises(ReadError) as caught:
        Parser(CATALOG).parse(broken)
    assert "2 号培养皿" in str(caught.value) and "未知怪物" in str(caught.value)
    broken = replace(f, lines=[line for line in f.lines if line.text != "15×12"])
    with pytest.raises(ReadError) as caught:
        Parser(CATALOG).parse(broken)
    assert "2 号培养皿 异端学者" in str(caught.value) and "匹配到 0 组" in str(caught.value)
    broken = replace(f, lines=[replace(line, text="589") if line.text == "588" else line for line in f.lines])
    with pytest.raises(ReadError, match="六槽合计 588，左侧识别为 589"):
        Parser(CATALOG).parse(broken)


def test_client_preserves_server_validation_details():
    async def run():
        client = Client("http://test.local")
        message = "可见数据无效：第 3 号培养皿 士兵 缺少有效的 activity（活性）"
        await client.http.aclose()
        client.http = httpx.AsyncClient(transport=httpx.MockTransport(
            lambda request: httpx.Response(400, json={"message": message})), base_url="http://test.local")
        try:
            with pytest.raises(ServiceError) as caught:
                await client.suggest({})
            assert str(caught.value) == message
        finally:
            await client.close()
    asyncio.run(run())


def test_tool_confirmation_refresh_box_and_reward_clock(tmp_path):
    opening, potion, reward = [sample(n) for n in (2, 3, 6)]
    s, _ = Session(2).prepare(replace(opening, refreshes=2), CATALOG)
    choose(s, "claw")
    duplicate, _ = s.prepare(replace(opening, refreshes=2), CATALOG)
    assert not duplicate.tools and duplicate.suggestion == s.suggestion
    refreshed = replace(opening, card_ids=["growth", "claw", "pituitary"], refreshes=1)
    s, _ = s.prepare(refreshed, CATALOG)
    assert s.tool_refreshes == 1 and not s.tools and s.suggestion is None
    choose(s, "pituitary")
    s, visible = s.prepare(potion, CATALOG)
    assert s.tools == ["pituitary"] and s.completed == 0 and s.base_cursor == 1
    assert visible["completedTurns"] == 0
    refreshed_potion = replace(potion, card_ids=list(reversed(potion.card_ids)), refreshes=2)
    s, _ = s.prepare(refreshed_potion, CATALOG)
    assert s.completed == 0 and s.potion_refreshes == 2 and s.tool_refreshes == 1
    # An arbitrary box result changes the offer but consumes no turn or refresh.
    s, _ = s.prepare(replace(refreshed_potion, card_ids=potion.card_ids), CATALOG)
    assert s.completed == 0 and s.potion_refreshes == 2
    s, _ = s.prepare(replace(reward, refreshes=1), CATALOG)
    assert s.completed == 1 and s.unlocked == 1 and s.base_cursor == 2
    choose(s, "saw")
    s, visible = s.prepare(replace(potion, round=3, unlocked=1, refreshes=2), CATALOG)
    assert s.tools == ["pituitary", "saw"] and s.completed == 2 and s.base_cursor == 2
    assert visible["toolClaims"][0]["status"] == "CLAIMED"
    s.save(tmp_path / "session.json")
    assert Session.load(tmp_path / "session.json") == s


def test_missed_frames_are_not_invented_tool_choices():
    opening, potion = sample(2), sample(3)
    with pytest.raises(ReadError, match="必须从第 1 回合"):
        Session(0).prepare(potion, CATALOG)
    s, _ = Session(0).prepare(opening, CATALOG)
    choose(s, "claw")
    s, _ = s.prepare(potion, CATALOG)
    gap, _ = s.prepare(replace(potion, round=4), CATALOG)
    assert gap.completed == 3 and gap.base_cursor == 4 and gap.warnings
    gap, visible = s.prepare(replace(potion, round=4, unlocked=1), CATALOG)
    assert gap.tools == ["claw"] and gap.unknown_tools == 1
    assert gap.completed == 3 and gap.base_cursor == 3
    assert visible["unknownTools"] == 1 and visible["toolClaims"][0]["status"] == "CLAIMED"
    assert any("漏扫" in message for message in gap.warnings)
    repeat, _ = gap.prepare(replace(potion, round=4, unlocked=1), CATALOG)
    assert repeat.unknown_tools == 1 and any("不模拟" in message for message in repeat.warnings)
    assert s.completed == 0 and s.tools == ["claw"]
    with pytest.raises(ReadError, match="内容版本"):
        s.prepare(potion, {**CATALOG, "rulesVersion": "new-rules"})
    migrated, visible = s.prepare(potion, {**CATALOG, "rulesVersion": "new-rules"}, test_mode=True)
    assert visible["rulesVersion"] == "new-rules"
    assert any("测试模式已将当前对局迁移" in warning for warning in migrated.warnings)
    # A recommendation to refresh is never recorded as an acquired tool.
    s, _ = Session(0).prepare(opening, CATALOG)
    s.suggestion = {"type": "refresh"}
    gap, visible = s.prepare(potion, CATALOG)
    assert not gap.tools and gap.unknown_tools == 1
    assert visible["baseCursor"] == 1 and visible["unknownTools"] == 1


def test_reward_recommendation_survives_round_eight_history_drift(tmp_path):
    # The saved round-8 tool page had incorrectly accumulated nine completed turns.
    reward = replace(sample(6), round=8, unlocked=2, card_ids=["saw", "nail"])
    s = Session(0, tools=["hatching_egg", "fang"], base_cursor=9, completed=9,
                unlocked=2, last=reward, rules_version=CATALOG["rulesVersion"],
                content_version=CATALOG["contentVersion"])
    choose(s, "saw")
    potion = replace(sample(3), round=9, unlocked=2)
    fixed, visible = s.prepare(potion, CATALOG)
    assert fixed.tools == ["hatching_egg", "fang", "saw"] and fixed.unknown_tools == 0
    assert fixed.base_cursor == 7 and fixed.completed == 8
    assert all(claim["status"] == "CLAIMED" for claim in visible["toolClaims"])
    assert visible["offer"] == {"kind": 2, "rewardThreshold": 0}
    assert any("校正" in message for message in fixed.warnings)
    fixed.save(tmp_path / "session.json")
    repeated, _ = Session.load(tmp_path / "session.json").prepare(potion, CATALOG)
    assert repeated.tools == fixed.tools and repeated.completed == 8
    assert s.tools == ["hatching_egg", "fang"]  # Preparing remains transactional.


def test_same_round_scene_changes_do_not_advance_progress():
    s, _ = Session(0).prepare(sample(2), CATALOG)
    choose(s, "claw")
    s, _ = s.prepare(sample(3), CATALOG)
    preview = replace(sample(3), score=sample(3).score + 10)
    s, _ = s.prepare(preview, CATALOG)
    assert s.base_cursor == 1 and s.completed == 0
    assert s.suggestion is None


def test_repeated_tool_acquisitions_are_saved_in_order(tmp_path):
    s, _ = Session(0).prepare(sample(2), CATALOG)
    choose(s, "claw")
    s, _ = s.prepare(sample(3), CATALOG)
    for reward_round, unlocked in ((2, 1), (4, 2)):
        reward = replace(sample(6), round=reward_round, unlocked=unlocked, card_ids=["claw", "saw", "eye"])
        s, _ = s.prepare(reward, CATALOG)
        choose(s, "claw")
        potion = replace(sample(3), round=reward_round + 1, unlocked=unlocked)
        s, _ = s.prepare(potion, CATALOG)
        assert s.tools == ["claw"] * (unlocked + 1) and s.unknown_tools == 0
        s, _ = s.prepare(potion, CATALOG)
        assert s.tools == ["claw"] * (unlocked + 1)
    s.save(tmp_path / "session.json")
    assert Session.load(tmp_path / "session.json").tools == ["claw", "claw", "claw"]


def test_missed_stage_is_calibrated_to_current_offer():
    s, _ = Session(0).prepare(sample(2), CATALOG)
    choose(s, "claw")
    s, _ = s.prepare(sample(3), CATALOG)
    s, visible = s.prepare(replace(sample(17), round=7, unlocked=0), CATALOG)
    assert visible["baseCursor"] == 9 and visible["completedTurns"] == 8
    assert visible["offer"]["kind"] == 4 and any("校正" in message for message in s.warnings)


def test_full_potion_and_scheme_sequence_and_display_contract():
    s, _ = Session(0).prepare(sample(2), CATALOG)
    choose(s, "claw")
    potion = sample(3)
    s, _ = s.prepare(potion, CATALOG)
    for turn in range(2, CATALOG["flow"]["potionTurns"] + 1):
        s, _ = s.prepare(replace(potion, round=turn), CATALOG)
    schemes = sample(17)
    for turn in range(9, 12):
        s, _ = s.prepare(replace(schemes, round=turn, unlocked=0), CATALOG)
    assert s.base_cursor == 11 and s.completed == 10
    result = action_text({"type": "choose", "cardId": "scheme_0", "targetSlots": []}, CATALOG, schemes.card_labels)
    assert "全身针灸疗法" in result and "建议" in result
    assert "每次新候选" in GUIDANCE and "开箱" in GUIDANCE and "奖励用具" in GUIDANCE


def test_http_failure_preserves_observation_and_retries_without_double_count(tmp_path):
    async def run():
        c = Controller("http://test", 0, tmp_path, tmp_path, 1)
        attempts = []

        def responder(request):
            if request.method == "GET":
                return httpx.Response(200, json=CATALOG)
            attempts.append(json.loads(request.content))
            if len(attempts) == 1:
                raise httpx.ConnectError("offline", request=request)
            return httpx.Response(200, json={"action": {"type": "choose", "cardId": "claw", "targetSlots": []}, "observation": {}})

        await c.client.http.aclose()
        c.client.http = httpx.AsyncClient(transport=httpx.MockTransport(responder), base_url="http://test")
        try:
            with pytest.raises(RuntimeError, match="连接服务端失败"):
                await c.accept(frame(2))
            assert c.session.last is not None and not c.session.tools
            response = await c.accept(frame(2))
            assert response["action"]["cardId"] == "claw" and len(attempts) == 2
            assert (await c.accept(frame(2)))["cached"] and len(attempts) == 2
            assert Session.load(tmp_path / "session.json").suggestion["cardId"] == "claw"
        finally:
            await c.close()
    asyncio.run(run())
