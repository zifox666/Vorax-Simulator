import ctypes
from ctypes import wintypes
import os
from pathlib import Path
import subprocess
import sys
import threading


def _api():
    if sys.platform != "win32":
        raise OSError("全局快捷键和窗口截图仅支持 Windows")
    user = ctypes.WinDLL("user32", use_last_error=True)
    functions = {
        "EnumWindows": ([ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM), wintypes.LPARAM], wintypes.BOOL),
        "GetWindowTextW": ([wintypes.HWND, wintypes.LPWSTR, ctypes.c_int], ctypes.c_int),
        "GetForegroundWindow": ([], wintypes.HWND),
        "IsIconic": ([wintypes.HWND], wintypes.BOOL),
        "IsWindowVisible": ([wintypes.HWND], wintypes.BOOL),
        "GetClientRect": ([wintypes.HWND, ctypes.POINTER(wintypes.RECT)], wintypes.BOOL),
        "ClientToScreen": ([wintypes.HWND, ctypes.POINTER(wintypes.POINT)], wintypes.BOOL),
        "SetThreadDpiAwarenessContext": ([ctypes.c_void_p], ctypes.c_void_p),
        "SetProcessDpiAwarenessContext": ([ctypes.c_void_p], wintypes.BOOL),
        "RegisterHotKey": ([wintypes.HWND, ctypes.c_int, wintypes.UINT, wintypes.UINT], wintypes.BOOL),
        "UnregisterHotKey": ([wintypes.HWND, ctypes.c_int], wintypes.BOOL),
        "GetMessageW": ([ctypes.POINTER(wintypes.MSG), wintypes.HWND, wintypes.UINT, wintypes.UINT], ctypes.c_int),
        "PeekMessageW": ([ctypes.POINTER(wintypes.MSG), wintypes.HWND, wintypes.UINT, wintypes.UINT, wintypes.UINT], wintypes.BOOL),
        "PostThreadMessageW": ([wintypes.DWORD, wintypes.UINT, wintypes.WPARAM, wintypes.LPARAM], wintypes.BOOL),
    }
    for name, (arguments, result) in functions.items():
        function = getattr(user, name)
        function.argtypes, function.restype = arguments, result
    return user


def enable_dpi():
    if sys.platform == "win32":
        _api().SetProcessDpiAwarenessContext(ctypes.c_void_p(-4))


def launch_arguments() -> list[str]:
    return sys.argv[1:] if getattr(sys, "frozen", False) else ["-m", "vorax_assistant", *sys.argv[1:]]


def ensure_admin() -> bool:
    """Return False after handing interactive startup to the UAC-launched copy."""
    if sys.platform != "win32":
        raise OSError("交互客户端仅支持 Windows")
    shell = ctypes.WinDLL("shell32", use_last_error=True)
    shell.IsUserAnAdmin.argtypes, shell.IsUserAnAdmin.restype = [], wintypes.BOOL
    if shell.IsUserAnAdmin():
        return True
    shell.ShellExecuteW.argtypes = [wintypes.HWND, wintypes.LPCWSTR, wintypes.LPCWSTR,
                                   wintypes.LPCWSTR, wintypes.LPCWSTR, ctypes.c_int]
    shell.ShellExecuteW.restype = ctypes.c_void_p
    result = shell.ShellExecuteW(None, "runas", sys.executable,
                                subprocess.list2cmdline(launch_arguments()), os.getcwd(), 1)
    if not result or result <= 32:
        raise OSError("需要管理员权限运行；授权已取消或启动失败，请右键选择“以管理员身份运行”")
    return False


def portrait_console(hosted: bool = False) -> bool:
    """Use an independent visible console when the terminal host owns sizing."""
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.GetConsoleWindow.argtypes, kernel.GetConsoleWindow.restype = [], wintypes.HWND
    window = kernel.GetConsoleWindow()
    if not hosted and not _api().IsWindowVisible(window):
        conhost = Path(os.environ["WINDIR"]) / "System32" / "conhost.exe"
        subprocess.Popen([str(conhost), sys.executable, *launch_arguments(), "--console-hosted"],
                         creationflags=subprocess.CREATE_NEW_CONSOLE)
        return False

    class Coord(ctypes.Structure):
        _fields_ = [("X", ctypes.c_short), ("Y", ctypes.c_short)]

    class SmallRect(ctypes.Structure):
        _fields_ = [(name, ctypes.c_short) for name in ("Left", "Top", "Right", "Bottom")]

    class BufferInfo(ctypes.Structure):
        _fields_ = [("size", Coord), ("cursor", Coord), ("attributes", wintypes.WORD),
                    ("window", SmallRect), ("maximum", Coord)]

    declarations = {
        "GetStdHandle": ([wintypes.DWORD], wintypes.HANDLE),
        "GetConsoleScreenBufferInfo": ([wintypes.HANDLE, ctypes.POINTER(BufferInfo)], wintypes.BOOL),
        "GetLargestConsoleWindowSize": ([wintypes.HANDLE], Coord),
        "SetConsoleWindowInfo": ([wintypes.HANDLE, wintypes.BOOL, ctypes.POINTER(SmallRect)], wintypes.BOOL),
        "SetConsoleScreenBufferSize": ([wintypes.HANDLE, Coord], wintypes.BOOL),
        "SetConsoleTitleW": ([wintypes.LPCWSTR], wintypes.BOOL),
    }
    for name, (arguments, result) in declarations.items():
        function = getattr(kernel, name)
        function.argtypes, function.restype = arguments, result
    output = kernel.GetStdHandle(-11)
    info = BufferInfo()
    if not kernel.GetConsoleScreenBufferInfo(output, ctypes.byref(info)):
        raise ctypes.WinError(ctypes.get_last_error())
    maximum = kernel.GetLargestConsoleWindowSize(output)
    rows = min(48, maximum.Y)
    columns = min(72, maximum.X, rows * 3 // 2)
    small = SmallRect(0, 0, min(columns, info.window.Right - info.window.Left + 1) - 1,
                      min(rows, info.window.Bottom - info.window.Top + 1) - 1)
    desired = SmallRect(0, 0, columns - 1, rows - 1)
    if (not kernel.SetConsoleWindowInfo(output, True, ctypes.byref(small))
            or not kernel.SetConsoleScreenBufferSize(output, Coord(columns, rows))
            or not kernel.SetConsoleWindowInfo(output, True, ctypes.byref(desired))):
        raise ctypes.WinError(ctypes.get_last_error())
    kernel.SetConsoleTitleW("渴瘾对局参考 · 管理员")
    return True


def find_game_window(user):
    def matches(window):
        title = ctypes.create_unicode_buffer(512)
        return (user.GetWindowTextW(window, title, len(title)) > 0
                and title.value.strip().casefold() == "torchlight: infinite")

    foreground = user.GetForegroundWindow()
    if foreground and matches(foreground):
        return foreground

    found = None

    @ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)
    def visit(window, _):
        nonlocal found
        if matches(window):
            found = window
            return False
        return True

    user.EnumWindows(visit, 0)
    return found


def capture():
    from PIL import ImageGrab

    user = _api()
    previous = user.SetThreadDpiAwarenessContext(ctypes.c_void_p(-4))
    try:
        window = find_game_window(user)
        if not window:
            raise OSError("未找到 Torchlight: Infinite 游戏窗口（已忽略标题首尾空白）")
        if user.IsIconic(window) or user.GetForegroundWindow() != window:
            raise OSError("请将游戏置于前台且不要最小化，再按 ~；窗口不要被其他窗口遮挡")
        rect = wintypes.RECT()
        origin = wintypes.POINT(0, 0)
        if not user.GetClientRect(window, ctypes.byref(rect)) or not user.ClientToScreen(window, ctypes.byref(origin)):
            raise ctypes.WinError(ctypes.get_last_error())
        if rect.right <= 0 or rect.bottom <= 0:
            raise OSError("游戏客户区尺寸无效")
        return ImageGrab.grab(bbox=(origin.x, origin.y, origin.x + rect.right, origin.y + rect.bottom), all_screens=True)
    finally:
        if previous:
            user.SetThreadDpiAwarenessContext(previous)


class Hotkey:
    def __init__(self, callback, on_error):
        self.callback, self.on_error = callback, on_error
        self.thread_id = None
        self.ready = threading.Event()
        self.thread = threading.Thread(target=self._listen, name="vorax-hotkey", daemon=True)

    def start(self):
        self.thread.start()

    def _listen(self):
        registered = []
        try:
            user = _api()
            self.thread_id = threading.get_native_id()
            message = wintypes.MSG()
            user.PeekMessageW(ctypes.byref(message), None, 0, 0, 0)
            self.ready.set()
            # Physical key to the left of 1, with or without Shift; no repeats.
            for identifier, modifiers in ((1, 0x4000), (2, 0x4004)):
                if not user.RegisterHotKey(None, identifier, modifiers, 0xC0):
                    raise OSError("无法注册 ~ 快捷键，可能被其他程序占用")
                registered.append(identifier)
            while True:
                result = user.GetMessageW(ctypes.byref(message), None, 0, 0)
                if result == -1:
                    raise ctypes.WinError(ctypes.get_last_error())
                if result == 0:
                    break
                if message.message == 0x0312:
                    self.callback()
        except OSError as exc:
            self.on_error(str(exc))
        finally:
            self.ready.set()
            for identifier in registered:
                user.UnregisterHotKey(None, identifier)

    def close(self):
        self.ready.wait(timeout=1)
        if self.thread_id and self.thread.is_alive():
            _api().PostThreadMessageW(self.thread_id, 0x0012, 0, 0)
            self.thread.join(timeout=1)
