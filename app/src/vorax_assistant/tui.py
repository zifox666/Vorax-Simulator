import asyncio
from dataclasses import replace
from datetime import datetime

from textual.app import App, ComposeResult
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.screen import ModalScreen
from textual.widgets import Button, Checkbox, DataTable, Footer, Header, Input, Select, Static

from .controller import Controller
from .settings import Settings, server_url
from .windows import Hotkey, capture


GUIDANCE = (
    "从第 1 回合的开局用具候选开始。每次新候选出现后请按 ~，包括刷新、开箱和奖励用具。\n"
    "手术用具请按推荐选择，页面推进后自动记录；药剂可自行选择。漏扫只提示并继续推演，未知用具效果不计入。\n"
    "开局用具不计回合、不触发回合结束效果；奖励用具计回合并触发效果。\n"
    "截图时将游戏置于前台，等待动画结束、移开遮挡。仅提供参考，不自动点击游戏。"
)


def action_text(action: dict | None, catalog: dict, labels: dict | None = None) -> str:
    if action is None:
        return "本局已完成"
    if action["type"] == "refresh":
        return "建议：刷新候选，然后再次按 ~"
    if action["type"] == "skip_unknown":
        return "建议：跳过未知器具"
    card = next(card for card in catalog["cards"] if card["id"] == action["cardId"])
    slots = action.get("targetSlots") or []
    target = " → ".join(f"{index + 1} 号" for index in slots) if slots else "无需指定培养皿"
    return f"建议：{(labels or {}).get(card['id'], card['name'])}\n目标：{target}"


class SettingsWindow(ModalScreen[Settings | None]):
    DEFAULT_CSS = """
    SettingsWindow { align: center middle; background: $background 70%; }
    #settings-dialog { width: 100%; max-width: 64; height: auto; max-height: 90%; padding: 1 2; border: round $accent; background: $surface; }
    #settings-title { text-style: bold; margin-bottom: 1; }
    #settings-server { margin: 1 0; }
    #settings-error { height: auto; color: $error; }
    #settings-actions { height: 3; margin-top: 1; }
    #settings-actions Button { width: 1fr; min-width: 8; }
    """
    BINDINGS = [("escape", "cancel", "取消")]

    def __init__(self, settings: Settings):
        super().__init__()
        self.settings = settings

    def compose(self) -> ComposeResult:
        with VerticalScroll(id="settings-dialog"):
            yield Static("设置", id="settings-title")
            yield Static("服务器地址（保存后立即切换，不清空对局）")
            yield Input(value=self.settings.server, placeholder="http://127.0.0.1:8080", id="settings-server")
            yield Checkbox("显示顶部操作提示", value=self.settings.show_guidance, id="settings-guidance")
            yield Static("", id="settings-error", markup=False)
            with Horizontal(id="settings-actions"):
                yield Button("保存", variant="primary", id="settings-save")
                yield Button("取消", id="settings-cancel")

    def action_cancel(self):
        self.dismiss(None)

    def save_settings(self):
        try:
            settings = Settings(server_url(self.query_one("#settings-server", Input).value),
                                self.query_one("#settings-guidance", Checkbox).value)
        except ValueError as exc:
            self.query_one("#settings-error", Static).update(str(exc))
            return
        self.dismiss(settings)

    def on_input_submitted(self, event: Input.Submitted):
        self.save_settings()

    def on_button_pressed(self, event: Button.Pressed):
        event.stop()
        if event.button.id == "settings-save":
            self.save_settings()
        elif event.button.id == "settings-cancel":
            self.action_cancel()


class Companion(App):
    TITLE = "渴瘾对局参考"
    CSS = """
    #main { padding: 0 1; }
    #guidance-panel { height: auto; padding: 0 1; border: round $warning; }
    #guidance { height: auto; }
    #hide-guidance { height: 3; width: 100%; }
    #controls { height: auto; margin: 1 0; }
    #pet { width: 100%; }
    #actions { height: 3; }
    #actions Button { width: 1fr; min-width: 8; }
    #server { height: auto; color: $text-muted; }
    #status { height: auto; min-height: 2; }
    #board { height: 10; }
    #tools { height: auto; margin: 1 0; }
    #result { height: auto; min-height: 4; padding: 1; border: round $accent; }
    #warnings { height: auto; color: $warning; }
    """
    BINDINGS = [("s", "settings", "设置"), ("g", "toggle_guidance", "提示"),
                ("n", "new_session", "新对局"), ("q", "quit", "退出")]

    def __init__(self, controller: Controller, settings: Settings | None = None, console_notice: str = ""):
        super().__init__()
        self.controller = controller
        self.settings = settings or Settings(str(controller.client.http.base_url).rstrip("/"))
        self.settings_path = controller.data / "settings.json"
        self.console_notice = console_notice
        self.busy = False
        self.hotkey = None

    def compose(self) -> ComposeResult:
        yield Header()
        with VerticalScroll(id="main"):
            with Vertical(id="guidance-panel"):
                yield Static(GUIDANCE, id="guidance", markup=False)
                yield Button("关闭顶部提示 (G)", id="hide-guidance")
            with Vertical(id="controls"):
                yield Select([(f"宠物用具刷新：{i} 次", i) for i in (0, 1, 2)], value=self.controller.session.pet, allow_blank=False, id="pet")
                with Horizontal(id="actions"):
                    yield Button("设置 (S)", id="settings")
                    yield Button("新对局 (N)", id="new")
                    yield Button("退出 (Q)", id="quit")
            yield Static(f"服务器：{self.settings.server}", id="server", markup=False)
            yield Static("正在连接服务端并加载本地 OCR 模型……", id="status", markup=False)
            yield DataTable(id="board")
            yield Static("已获得用具：尚未记录", id="tools", markup=False)
            yield Static("等待识别", id="result", markup=False)
            yield Static("", id="warnings", markup=False)
        yield Footer()

    async def on_mount(self):
        self.query_one("#guidance-panel").display = self.settings.show_guidance
        board = self.query_one("#board", DataTable)
        board.add_columns("培养皿", "怪物", "活性", "数量", "总活性")
        self.hotkey = Hotkey(lambda: self.call_from_thread(self.start_scan),
                             lambda message: self.call_from_thread(self.show_error, message))
        self.hotkey.start()
        self.run_worker(self.initialize(), exclusive=True, group="scan")

    async def initialize(self):
        self.busy = True
        try:
            await self.controller.initialize()
            restored = self.controller.session.last is not None
            self.query_one("#status", Static).update("已恢复本地记录，请识别当前游戏页面。" if restored else "准备就绪：切回游戏，按 ~ 识别。")
            self.query_one("#pet", Select).disabled = restored
            if self.console_notice:
                self.query_one("#warnings", Static).update(self.console_notice)
        except Exception as exc:
            self.show_error(str(exc))
        finally:
            self.busy = False

    def show_error(self, message: str):
        self.query_one("#status", Static).update(f"未完成：{message}")
        self.query_one("#result", Static).update("当前没有可用建议，请处理提示后重新识别")

    def start_scan(self):
        if not self.busy and not isinstance(self.screen, SettingsWindow):
            self.busy = True
            self.run_worker(self.scan(), exclusive=True, group="scan")

    async def scan(self):
        try:
            self.query_one("#result", Static).update("正在识别……")
            self.query_one("#warnings", Static).update("")
            if self.controller.reader is None:
                await self.controller.initialize()
            if self.controller.session.last is None:
                self.controller.session.pet = int(self.query_one("#pet", Select).value)
                self.controller.session.tool_refreshes = self.controller.session.pet
            picture = await asyncio.to_thread(capture)
            frame = await asyncio.to_thread(self.controller.reader.read, picture)
            response = await self.controller.accept(frame)
            self.show_snapshot(response)
        except Exception as exc:
            self.show_error(str(exc))
        finally:
            self.busy = False

    def show_snapshot(self, response: dict):
        session = self.controller.session
        snapshot = session.last
        catalog = self.controller.catalog
        board = self.query_one("#board", DataTable)
        board.clear()
        names = {monster["id"]: monster["name"] for monster in catalog["monsters"]}
        for slot in snapshot.slots:
            board.add_row(str(slot["index"] + 1), names.get(slot["definitionId"], "空"),
                          str(slot["activity"]), str(slot["quantity"]), str(slot["activity"] * slot["quantity"]))
        tool_names = {card["id"]: card["name"] for card in catalog["cards"]}
        tool_text = " → ".join(tool_names[t] for t in session.tools) or "尚无已知用具"
        if session.unknown_tools:
            tool_text += f"；另有 {session.unknown_tools} 件未知用具（不模拟效果）"
        self.query_one("#tools", Static).update("已记录用具（已知顺序）：" + tool_text)
        self.query_one("#status", Static).update(
            f"{datetime.now():%H:%M:%S} · 画面回合 {snapshot.round}/13 · 最终活性 {snapshot.score:,} · "
            f"药剂刷新 {session.potion_refreshes if session.potion_refreshes is not None else '未读到'} / "
            f"用具刷新 {session.tool_refreshes if session.tool_refreshes is not None else '未读到'}"
        )
        self.query_one("#result", Static).update(action_text(response["action"], catalog, snapshot.card_labels))
        self.query_one("#warnings", Static).update("\n".join(session.warnings))
        self.query_one("#pet", Select).disabled = True

    async def action_new_session(self):
        if self.busy or isinstance(self.screen, SettingsWindow):
            return
        await self.controller.new_session(int(self.query_one("#pet", Select).value))
        self.query_one("#pet", Select).disabled = False
        self.query_one("#board", DataTable).clear()
        self.query_one("#result", Static).update("旧记录已归档。设置宠物次数后，从第 1 回合开局用具开始识别。")
        self.query_one("#tools", Static).update("已获得用具：尚未记录")
        self.query_one("#warnings", Static).update("")

    def action_settings(self):
        if isinstance(self.screen, SettingsWindow):
            return
        if self.busy:
            self.query_one("#status", Static).update("正在识别或连接，完成后可打开设置。")
            return
        self.push_screen(SettingsWindow(self.settings), self.apply_settings)

    async def apply_settings(self, settings: Settings | None):
        if settings is None:
            return
        try:
            settings.save(self.settings_path)
            changed = settings.server != self.settings.server
            if changed:
                await self.controller.change_server(settings.server)
            self.settings = settings
            self.query_one("#guidance-panel").display = settings.show_guidance
            self.query_one("#server", Static).update(f"服务器：{settings.server}")
            if changed:
                self.query_one("#result", Static).update("服务器已切换，对局记录保留；连接成功后请重新识别。")
                self.query_one("#status", Static).update("正在连接新服务器……")
                self.busy = True
                self.run_worker(self.initialize(), exclusive=True, group="scan")
        except (OSError, ValueError, RuntimeError) as exc:
            self.show_error(str(exc))

    async def action_toggle_guidance(self):
        if not isinstance(self.screen, SettingsWindow):
            await self.apply_settings(replace(self.settings, show_guidance=not self.settings.show_guidance))

    async def on_button_pressed(self, event: Button.Pressed):
        if event.button.id == "new":
            await self.action_new_session()
        elif event.button.id == "quit":
            self.exit()
        elif event.button.id == "settings":
            self.action_settings()
        elif event.button.id == "hide-guidance":
            await self.action_toggle_guidance()

    async def on_unmount(self):
        if self.hotkey:
            await asyncio.to_thread(self.hotkey.close)
        await self.controller.close()
