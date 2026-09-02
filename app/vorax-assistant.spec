from pathlib import Path
from PyInstaller.utils.hooks import collect_data_files, collect_submodules

root = Path(SPECPATH)
a = Analysis(
    [str(root / "launcher.py")],
    pathex=[str(root / "src")],
    binaries=[],
    datas=(collect_data_files("rapidocr")
           + collect_data_files("stable_baselines3", includes=["version.txt"])
           + collect_data_files("sb3_contrib", includes=["version.txt"])),
    hiddenimports=(collect_submodules("rapidocr.inference_engine.onnxruntime")
                   + collect_submodules("vorax_gym")
                   + collect_submodules("sb3_contrib")),
    hookspath=[], hooksconfig={}, runtime_hooks=[], excludes=[], noarchive=False,
)
pyz = PYZ(a.pure)
exe = EXE(
    pyz, a.scripts, [], exclude_binaries=True, name="VoraxAssistant",
    debug=False, bootloader_ignore_signals=False, strip=False, upx=False,
    console=True, disable_windowed_traceback=False,
    uac_admin=True,
)
coll = COLLECT(exe, a.binaries, a.datas, strip=False, upx=False, name="VoraxAssistant")
