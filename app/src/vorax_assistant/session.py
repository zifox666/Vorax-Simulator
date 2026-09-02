from copy import deepcopy
from dataclasses import asdict, dataclass, field
import json
from pathlib import Path

from .parser import ReadError, Snapshot


@dataclass
class Session:
    pet: int
    tools: list[str] = field(default_factory=list)
    base_cursor: int = 0
    completed: int = 0
    unlocked: int = 0
    potion_refreshes: int | None = 3
    tool_refreshes: int | None = None
    last: Snapshot | None = None
    suggestion: dict | None = None
    rules_version: str = ""
    content_version: str = ""
    revision: int = 0
    warnings: list[str] = field(default_factory=list)
    unknown_tools: int = 0

    def __post_init__(self):
        if self.pet not in (0, 1, 2):
            raise ValueError("宠物用具刷新次数只能为 0、1、2")
        if self.last is None:
            self.tool_refreshes = self.pet

    def prepare(self, snapshot: Snapshot, catalog: dict, test_mode: bool = False) -> tuple["Session", dict]:
        candidate = deepcopy(self)
        candidate.warnings = []
        candidate._advance(snapshot, catalog, test_mode)
        return candidate, candidate.visible(catalog)

    def _advance(self, current: Snapshot, catalog: dict, test_mode: bool = False):
        previous_progress = (self.base_cursor, self.completed, self.unlocked, self.unknown_tools, tuple(self.tools))
        opening_offer = self.last is None
        if self.last is None:
            if current.round != 1 or current.kind != 3 or current.unlocked != 0:
                raise ReadError("必须从第 1 回合的开局用具候选开始记录；不能补猜已获得用具")
            self.rules_version = catalog["rulesVersion"]
            self.content_version = catalog["contentVersion"]
        else:
            versions = (catalog["rulesVersion"], catalog["contentVersion"])
            if (self.rules_version, self.content_version) != versions:
                if not test_mode:
                    raise ReadError("服务端内容版本已变化，请从下一局重新记录")
                known_tools = {card["id"] for card in catalog.get("cards", []) if card.get("kind") == 3}
                removed_tools = [tool for tool in self.tools if tool not in known_tools]
                if removed_tools:
                    self.tools = [tool for tool in self.tools if tool in known_tools]
                    self.unknown_tools += len(removed_tools)
                self.rules_version, self.content_version = versions
                self.suggestion = None
                message = "测试模式已将当前对局迁移到服务端最新内容，旧建议已清除"
                if removed_tools:
                    message += f"，{len(removed_tools)} 件已移除用具改记为未知"
                self.warnings.append(message)
            old = self.last
            delta = current.round - old.round
            if delta < 0 or current.unlocked < old.unlocked:
                raise ReadError("回合或奖励记录回退；新对局请按 N 重置，误识别请重新截图")
            # Same-round animations, previews and boxes must not consume turns.
            advanced = delta > 0 or current.kind != old.kind
            opening = old.kind == 3 and self.base_cursor == 0
            opening_offer = opening and not advanced
            if advanced:
                if old.kind == 3:
                    card_id = (self.suggestion or {}).get("cardId")
                    if ((self.suggestion or {}).get("type") == "choose"
                            and card_id in old.card_ids):
                        self.tools.append(card_id)
                        name = next(card["name"] for card in catalog["cards"] if card["id"] == card_id)
                        self.warnings.append(f"已按上一阶段推荐记录用具：{name}")
                    else:
                        self.unknown_tools += 1
                        self.warnings.append("缺少上一阶段可记录的用具推荐，已记为未知用具并继续推演")
                extra = max(0, delta - (0 if opening else 1))
                if extra:
                    self.warnings.append(f"漏扫 {extra} 个回合，已按当前 OCR 场面继续；不补猜药剂和目标")
            elif current.card_ids != old.card_ids:
                self.warnings.append("同回合候选已变化：按新候选重算，不计回合，也不假定执行了上次建议")
            if not advanced and current.kind == 3 and current.card_ids != old.card_ids:
                # A tool refresh invalidates the old offered choice.
                self.suggestion = None

        claimed = max(len(self.tools) + self.unknown_tools - 1, 0)
        self.unlocked = current.unlocked
        if current.kind != 3:
            missing = max(0, 1 + self.unlocked - len(self.tools) - self.unknown_tools)
            if missing:
                self.unknown_tools += missing
                claimed = self.unlocked
                self.warnings.append(f"可能漏扫了 {missing} 次奖励用具选择，已记为未知用具并继续推演")
        if claimed > self.unlocked:
            self.unlocked = claimed
            self.warnings.append("奖励数字与已记录用具不一致，保留已记录用具继续推演")
        if current.kind == 3 and not opening_offer and self.unlocked <= claimed:
            if claimed >= 2:
                raise ReadError("奖励用具已全部记录，但当前仍识别为用具候选；请确认页面或按 N 开始新局")
            self.unlocked = claimed + 1
            self.warnings.append("用具阶段与奖励数字不一致，按当前用具候选保留待领取奖励并继续推演")
        potion_turns = catalog["flow"]["potionTurns"]
        end_cursor = 1 + potion_turns + catalog["flow"]["schemeTurns"]
        # OCR rounds already include reward-tool turns; do not add them again.
        cursor = 0 if opening_offer else current.round - claimed
        low, high = (0, 0) if opening_offer else (1, end_cursor)
        if current.kind == 2:
            low, high = 1, potion_turns
        elif current.kind == 4:
            low, high = potion_turns + 1, end_cursor - 1
        self.base_cursor = min(max(cursor, low), high)
        self.completed = max(self.base_cursor - 1, 0) + claimed
        history_drifted = (self.last is not None and previous_progress[0] != 0
                           and previous_progress[1] != self.last.round - 1)
        if self.base_cursor != cursor or history_drifted:
            self.warnings.append("阶段或回合记录存在偏差（可能漏扫），已按当前 OCR 回合和候选阶段校正，继续推演")
        if self.unknown_tools:
            self.warnings.append(f"有 {self.unknown_tools} 件用具名称未知：保留领取进度，不模拟其后续效果，推荐可能有偏差")
        if current.kind == 4:
            self.warnings.append("手术方案仅按回合结束效果推荐；实际战斗风险和词缀未纳入模拟，请自行权衡")

        attr = "potion_refreshes" if current.kind == 2 else "tool_refreshes"
        if current.kind in (2, 3):
            maximum = 3 if current.kind == 2 else self.pet
            previous = getattr(self, attr)
            if current.refreshes is not None:
                if current.refreshes > maximum or previous is not None and current.refreshes > previous:
                    raise ReadError("剩余刷新次数增加，可能宠物配置有误、OCR 误识别或已经换局")
                setattr(self, attr, current.refreshes)
            elif previous != 0:
                setattr(self, attr, None)
                self.warnings.append("未读到剩余刷新数字，本类刷新暂按 0 次计算；可重新识别")
        progress = (self.base_cursor, self.completed, self.unlocked, self.unknown_tools, tuple(self.tools))
        if current != self.last or progress != previous_progress:
            self.suggestion = None
            self.revision += 1
        self.last = current

    def visible(self, catalog: dict) -> dict:
        current = self.last
        if current is None:
            raise ReadError("尚未开始记录")
        claimed = max(len(self.tools) + self.unknown_tools - 1, 0)
        claims = [{"threshold": threshold, "status": "CLAIMED" if i < claimed else "PENDING" if i < self.unlocked else "LOCKED"}
                  for i, threshold in enumerate((8000, 28000))]
        threshold = next((claim["threshold"] for claim in claims if claim["status"] == "PENDING"), 0)
        return {
            "rulesVersion": self.rules_version, "contentVersion": self.content_version,
            "phase": "CHOOSING", "baseCursor": self.base_cursor, "completedTurns": self.completed,
            "score": current.score, "slots": current.slots, "tools": self.tools,
            **({"unknownTools": self.unknown_tools} if self.unknown_tools else {}),
            "offer": {"kind": current.kind, "rewardThreshold": threshold if current.kind == 3 else 0},
            "cardIds": current.card_ids, "potionRefreshes": self.potion_refreshes or 0,
            "toolRefreshes": self.tool_refreshes or 0, "toolClaims": claims,
        }

    def save(self, path: Path):
        path.parent.mkdir(parents=True, exist_ok=True)
        temporary = path.with_suffix(".tmp")
        temporary.write_text(json.dumps(asdict(self), ensure_ascii=False, indent=2), encoding="utf-8")
        temporary.replace(path)

    @classmethod
    def load(cls, path: Path) -> "Session":
        data = json.loads(path.read_text(encoding="utf-8"))
        if data.get("last"):
            data["last"] = Snapshot(**data["last"])
        return cls(**data)
