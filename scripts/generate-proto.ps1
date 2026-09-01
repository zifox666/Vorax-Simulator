$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
    if ($LASTEXITCODE -ne 0) { throw 'Unable to install protoc-gen-go' }
    $taskGoBin = Join-Path (go env GOPATH) 'bin'
    $env:PATH = "$taskGoBin;$env:PATH"
protoc --go_out=. --go_opt=module=vorax api/game.proto api/training.proto
    if ($LASTEXITCODE -ne 0) { throw 'Protobuf generation failed' }
} finally { Pop-Location }
