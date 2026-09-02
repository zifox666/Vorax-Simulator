$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    uv sync --locked --python 3.12 --extra local-model
    if ($LASTEXITCODE -ne 0) { throw 'Python dependency installation failed' }
    uv run pyinstaller --noconfirm vorax-assistant.spec
    if ($LASTEXITCODE -ne 0) { throw 'PyInstaller build failed' }
    $modelTarget = Join-Path $PSScriptRoot 'dist/VoraxAssistant/model'
    New-Item -ItemType Directory -Path $modelTarget -Force | Out-Null
    foreach ($modelName in @('ch_PP-OCRv6_det_infer.onnx', 'ch_PP-OCRv6_rec_infer.onnx', 'ch_ppocrv6_dict.txt')) {
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot "model/$modelName") -Destination $modelTarget
    }
    # Decision models are user-supplied and deliberately never copied into the build.
    New-Item -ItemType Directory -Path (Join-Path $PSScriptRoot 'dist/VoraxAssistant/models') -Force | Out-Null
    Write-Output 'Built: dist/VoraxAssistant/VoraxAssistant.exe (distribute the entire directory)'
} finally {
    Pop-Location
}
