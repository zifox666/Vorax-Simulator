import argparse
import asyncio
import json
import os
from pathlib import Path
import sys


def main():
    base = Path(sys.executable).parent if getattr(sys, "frozen", False) else Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description="渴瘾对局参考：本地 OCR + 服务端推演")
    parser.add_argument("--server", help="临时覆盖已保存的服务器地址")
    parser.add_argument("--console-hosted", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--pet", type=int, choices=(0, 1, 2), default=0)
    parser.add_argument("--models", type=Path, default=base / "model")
    parser.add_argument("--data", type=Path, default=Path(os.environ.get("LOCALAPPDATA", str(base / ".local"))) / "VoraxAssistant")
    parser.add_argument("--rollouts", type=int, choices=range(1, 65), default=16, metavar="1..64")
    source = parser.add_mutually_exclusive_group()
    source.add_argument("--image", type=Path, help="离线读取图片，不操作游戏窗口")
    source.add_argument("--ocr-json", type=Path, help="直接读取包含文字四点坐标的 OCR JSON")
    parser.add_argument("--ocr-only", action="store_true", help="只输出 OCR JSON，不连接服务端")
    parser.add_argument("--output", type=Path, help="离线模式输出 JSON 文件")
    parser.add_argument("--new", action="store_true", help="归档已有记录后开始新局")
    args = parser.parse_args()
    if args.ocr_only and not (args.image or args.ocr_json):
        parser.error("--ocr-only 需要 --image 或 --ocr-json")
    from .windows import enable_dpi
    enable_dpi()
    try:
        from .settings import Settings, server_url
        settings = Settings.load(args.data / "settings.json")
        if args.server is not None:
            settings.server = server_url(args.server)
        args.server = settings.server
        if args.image or args.ocr_json:
            result = asyncio.run(offline(args))
            text = json.dumps(result, ensure_ascii=False, indent=2)
            if args.output:
                args.output.parent.mkdir(parents=True, exist_ok=True)
                args.output.write_text(text, encoding="utf-8")
            else:
                print(text)
        else:
            from .windows import ensure_admin, portrait_console
            if not ensure_admin():
                return
            console_notice = ""
            try:
                if not portrait_console(args.console_hosted):
                    return
            except OSError as exc:
                console_notice = f"默认竖向窗口设置失败，可手动调整窗口：{exc}"
            from .controller import Controller
            from .tui import Companion
            controller = Controller(args.server, args.pet, args.models, args.data, args.rollouts)
            if args.new:
                asyncio.run(controller.new_session(args.pet))
            Companion(controller, settings, console_notice).run()
    except (OSError, ValueError, RuntimeError) as exc:
        print(f"未完成：{exc}", file=sys.stderr)
        raise SystemExit(1) from exc


async def offline(args):
    from .ocr import Frame, Reader

    if args.ocr_json:
        frame = Frame.from_dict(json.loads(args.ocr_json.read_text(encoding="utf-8")))
    else:
        from PIL import Image
        with Image.open(args.image) as picture:
            frame = Reader(args.models).read(picture.convert("RGB"))
    if args.ocr_only:
        return frame.to_dict()
    from .controller import Controller
    controller = Controller(args.server, args.pet, args.models, args.data, args.rollouts)
    try:
        if args.new:
            await controller.new_session(args.pet)
        result = await controller.accept(frame)
        result["warnings"] = controller.session.warnings
        return result
    finally:
        await controller.close()


if __name__ == "__main__":
    main()
