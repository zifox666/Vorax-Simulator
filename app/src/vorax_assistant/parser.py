import re
import unicodedata
from dataclasses import asdict, dataclass, field
from difflib import SequenceMatcher
from statistics import median

from .ocr import Frame, Line


class ReadError(ValueError):
    """The OCR frame cannot safely represent a stable game screen."""


def ocr_text(lines: list[Line]) -> str:
    texts = list(dict.fromkeys(line.text for line in lines))
    preview = "、".join(f"「{text[:80]}」" for text in texts[:8])
    return (preview + ("……" if len(texts) > 8 else "")) or "无"


def normalized(text: str) -> str:
    text = re.sub(r"\s", "", unicodedata.normalize("NFKC", text))
    text = re.sub(r"一(?=骨卫兵|觉醒者|异魔|蛊虫)", "-", text)
    return re.sub(r"[\s\-—–_:：()（）·]", "", text)


def match_name(text: str, entries: list[dict]) -> dict | None:
    key = normalized(text)
    exact = [entry for entry in entries if normalized(entry["name"]) == key]
    if exact:
        return exact[0]
    # Do not reduce an unknown compound name to a known short suffix.
    candidates = []
    for entry in sorted(entries, key=lambda e: len(e["name"]), reverse=True):
        name = normalized(entry["name"])
        if min(len(name), len(key)) < 4 or abs(len(name) - len(key)) > 1:
            continue
        ratio = SequenceMatcher(None, key, name).ratio()
        if ratio >= 0.8:
            candidates.append((ratio, entry))
    candidates.sort(key=lambda item: item[0], reverse=True)
    if candidates and (len(candidates) == 1 or candidates[0][0] - candidates[1][0] >= 0.12):
        return candidates[0][1]
    return None


def integer(text: str) -> int | None:
    text = unicodedata.normalize("NFKC", text).replace(" ", "").replace(",", "")
    # The red activity icon immediately before a total is occasionally
    # recognized as a currency symbol (for example "$36").  Only tolerate
    # that single, known prefix; arbitrary non-numeric OCR remains rejected.
    text = re.sub(r"^[\$¥￥](?=\d+$)", "", text)
    return int(text) if re.fullmatch(r"\d+", text) else None


def product(text: str) -> tuple[int, int] | None:
    text = unicodedata.normalize("NFKC", text).replace(" ", "").replace(",", "")
    found = re.fullmatch(r"(\d+)[xX×✕*](\d+)", text)
    return tuple(map(int, found.groups())) if found else None


@dataclass
class Snapshot:
    round: int
    kind: int
    score: int
    slots: list[dict]
    card_ids: list[str]
    refreshes: int | None
    unlocked: int
    card_labels: dict[str, str] = field(default_factory=dict)

    def to_dict(self) -> dict:
        return asdict(self)


class Parser:
    def __init__(self, catalog: dict):
        self.catalog = catalog

    def parse(self, frame: Frame) -> Snapshot:
        lines = frame.lines
        title = next((line for line in lines if normalized(line.text) == "手术准备"), None)
        if title is None:
            raise ReadError("未识别到手术准备页面，请关闭遮挡后重新识别")
        heading = next((line for line in lines if "选择一种" in normalized(line.text)), None)
        if heading is None:
            raise ReadError("尚未显示完整候选，请等动画结束后再按 ~；结算后可按 N 开始下一局")
        label = normalized(heading.text)
        if "用具" in label:
            kind = 3
        elif "药剂" in label:
            kind = 2
        elif "方案" in label:
            kind = 4
        else:
            raise ReadError(f"无法识别候选阶段：{heading.text}")

        round_line = next((line for line in lines if normalized(line.text) == "回合"), None)
        if round_line is None:
            raise ReadError("回合文字未识别，请重新识别")
        side = [line for line in lines if abs(line.x - round_line.x) < frame.width * 0.07]
        round_values = []
        for line in side:
            found = re.fullmatch(r"(\d{1,2})/13", normalized(line.text))
            if found and round_line.y < line.y < round_line.y + frame.height * 0.12:
                round_values.append(int(found[1]))
        if len(round_values) != 1 or not 1 <= round_values[0] <= 13:
            raw = [line for line in side if round_line.y < line.y < round_line.y + frame.height * 0.12]
            raise ReadError(f"无法读取 X / 13 回合数：匹配到 {round_values}；该区域 OCR：{ocr_text(raw)}")

        score_label = next((line for line in side if "最终活性" in normalized(line.text)), None)
        reward_label = next((line for line in side if "下档奖励" in normalized(line.text)), None)
        if score_label is None or reward_label is None:
            missing = [name for name, line in (("最终活性", score_label), ("下档奖励", reward_label)) if line is None]
            raise ReadError(f"缺少文字：{'、'.join(missing)}；请重新识别")
        scores = [integer(line.text) for line in side if score_label.y < line.y < reward_label.y
                  and integer(line.text) is not None]
        if len(scores) != 1:
            raw = [line for line in side if score_label.y < line.y < reward_label.y]
            raise ReadError(f"最终活性需要 1 个数值，匹配到 {len(scores)} 个 {scores}；该区域 OCR：{ocr_text(raw)}")
        counts = [re.fullmatch(r"([012])/2", normalized(line.text)) for line in side
                  if line.y > reward_label.y]
        counts = [int(count[1]) for count in counts if count]
        if len(counts) != 1:
            raw = [line for line in side if line.y > reward_label.y]
            raise ReadError(f"奖励用具 X / 2 需要 1 条，匹配到 {len(counts)} 条 {counts}；该区域 OCR：{ocr_text(raw)}")

        slots = self._slots(frame, title, round_line, heading)
        total = sum(s["activity"] * s["quantity"] for s in slots)
        if total != scores[0]:
            raise ReadError(f"六槽总活性与左侧最终活性不一致：六槽合计 {total}，左侧识别为 {scores[0]}；请等动画结束后重新识别")
        cards, card_labels = self._cards(frame, heading, kind)
        confirm = next((line for line in lines if normalized(line.text) == "确定"
                        and line.y > heading.y), None)
        refresh = None
        if confirm is not None and kind in (2, 3):
            numbers = [integer(line.text) for line in lines
                       if confirm.x + frame.width * 0.06 < line.x < confirm.x + frame.width * 0.23
                       and abs(line.y - confirm.y) < max(confirm.height, frame.height * 0.025)
                       and integer(line.text) is not None]
            if len(numbers) == 1 and 0 <= numbers[0] <= 3:
                refresh = numbers[0]
        return Snapshot(round_values[0], kind, scores[0], slots, cards, refresh, counts[0], card_labels)

    def _slots(self, frame: Frame, title: Line, round_line: Line, heading: Line) -> list[dict]:
        title_width = max(p[0] for p in title.box) - min(p[0] for p in title.box)
        first = max(p[0] for p in title.box) + 0.80 * title_width
        pitch = 1.50 * title_width
        band = [line for line in frame.lines if first - pitch * 0.48 < line.x < first + pitch * 5.48
                and round_line.y - frame.height * 0.04 < line.y < min(heading.y, round_line.y + frame.height * 0.15)]
        names = [(line, match_name(line.text, self.catalog["monsters"])) for line in band]
        names = [(line, name) for line, name in names if name is not None]
        if not names:
            raise ReadError(f"未识别到培养皿怪物，不能把漏识别当作空槽；该区域 OCR：{ocr_text(band)}")
        row = median(line.y for line, _ in names)
        names = [(line, name) for line, name in names if abs(line.y - row) < frame.height * 0.025]
        indexed = [(round((line.x - first) / pitch), line, name) for line, name in names]
        if any(not 0 <= i < 6 or abs(line.x - first - pitch * i) > pitch * 0.24 for i, line, _ in indexed):
            raise ReadError("培养皿文字位置无法对应六个固定槽位，请使用完整游戏窗口")
        # Fit text centers without collapsing gaps; title anchor fixes slot identity.
        if len({i for i, _, _ in indexed}) >= 2:
            mean_i = sum(i for i, _, _ in indexed) / len(indexed)
            mean_x = sum(line.x for _, line, _ in indexed) / len(indexed)
            pitch = sum((i - mean_i) * (line.x - mean_x) for i, line, _ in indexed) / sum((i - mean_i)**2 for i, _, _ in indexed)
            first = mean_x - pitch * mean_i
        result = []
        for index in range(6):
            column = [line for line in band if abs(line.x - first - index * pitch) < pitch * 0.40
                      and row - frame.height * 0.022 < line.y < row + frame.height * 0.075]
            found = [(line, name) for i, line, name in indexed if i == index]
            if not found:
                if column:
                    raise ReadError(f"{index + 1} 号培养皿有文字但名称不明，不能判为空槽；该槽 OCR：{ocr_text(column)}")
                result.append({"index": index, "definitionId": "", "activity": 0, "quantity": 0})
                continue
            if len(found) != 1:
                raise ReadError(f"{index + 1} 号培养皿名称重复：{ocr_text([line for line, _ in found])}")
            name_line, name = found[0]
            values = [(line, product(line.text)) for line in column if line.y > name_line.y]
            values = [(line, value) for line, value in values if value is not None]
            if len(values) != 1:
                raise ReadError(f"{index + 1} 号培养皿 {name['name']} 需要 1 组活性 × 数量，匹配到 {len(values)} 组；该槽 OCR：{ocr_text(column)}")
            product_line, (activity, quantity) = values[0]
            totals = [integer(line.text) for line in column if name_line.y < line.y < product_line.y
                      and integer(line.text) is not None]
            if len(totals) != 1 or activity <= 0 or quantity <= 0 or totals[0] != activity * quantity:
                raise ReadError(f"{index + 1} 号培养皿 {name['name']} 数值不一致：活性 {activity} × 数量 {quantity} = {activity * quantity}，总活性识别为 {totals}；请重新识别")
            result.append({"index": index, "definitionId": name["id"], "activity": activity, "quantity": quantity})
        return result

    def _cards(self, frame: Frame, heading: Line, kind: int) -> tuple[list[str], dict[str, str]]:
        pool = [card for card in self.catalog["cards"] if card["kind"] == kind]
        area = [line for line in frame.lines if line.y > heading.y + frame.height * 0.04
                and line.x > frame.width * 0.27]
        if kind == 4:
            footers = sorted((line for line in area if normalized(line.text) in
                              ("普通手术方案", "魔法手术方案", "稀有手术方案")), key=lambda line: line.x)
            if len(footers) != 3:
                missing = [name for name in ("普通手术方案", "魔法手术方案", "稀有手术方案")
                           if not any(normalized(line.text) == name for line in footers)]
                raise ReadError(f"手术方案卡未完整识别：匹配到 {len(footers)}/3 张；缺少：{'、'.join(missing) or '无（检查重复识别）'}")
            labels = {}
            for card, footer in zip(sorted(pool, key=lambda card: card["id"]), footers):
                titles = [line for line in area if abs(line.x - footer.x) < frame.width * 0.055
                          and line.y < footer.y - frame.height * 0.06
                          and len(re.findall(r"[\u4e00-\u9fff]", line.text)) >= 3]
                if not titles:
                    raise ReadError(f"{footer.text} 上方的手术方案名称未完整识别")
                labels[card["id"]] = min(titles, key=lambda line: line.y).text
            # Schemes only model end-of-turn effects. Preserve actual OCR titles.
            return list(labels), labels
        matches = [(line, match_name(line.text, pool)) for line in area]
        matches = [(line, card) for line, card in matches if card is not None]
        if kind != 4 and matches:
            row = min(line.y for line, _ in matches)
            matches = [(line, card) for line, card in matches if abs(line.y - row) < frame.height * 0.035]
        matches.sort(key=lambda pair: pair[0].x)
        ids = [card["id"] for _, card in matches]
        if len(ids) not in (3, 5) or len(set(ids)) != len(ids):
            raw = sorted(area, key=lambda line: (line.y, line.x))
            if matches:
                row = min(line.y for line, _ in matches)
                raw = [line for line in raw if abs(line.y - row) < frame.height * 0.025]
            unmatched = [line for line in raw if match_name(line.text, pool) is None]
            names = list(dict.fromkeys(card["name"] for _, card in matches))
            duplicates = list(dict.fromkeys(card["name"] for _, card in matches if ids.count(card["id"]) > 1))
            raise ReadError(
                f"候选卡未完整识别：匹配到 {len(ids)} 张（需要 3 或 5 张）；"
                f"已识别：{'、'.join(names) or '无'}；重复匹配：{'、'.join(duplicates) or '无'}；"
                f"未匹配 OCR 原文：{ocr_text(unmatched)}。请重新识别；原文正确但未匹配时请刷新或补充卡牌目录"
            )
        return ids, {card["id"]: card["name"] for _, card in matches}
