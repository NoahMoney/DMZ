# PowerShell twin of run.sh. Usage:  .\tests\run.ps1 [-run Fuzz -v]
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Dst = Join-Path $Root "vendor\derohe\veil"
if (Test-Path $Dst) { Remove-Item -Recurse -Force $Dst }
New-Item -ItemType Directory -Force $Dst | Out-Null
Copy-Item -Recurse (Join-Path $Root "reference") (Join-Path $Dst "reference")
Copy-Item -Recurse (Join-Path $Root "tests") (Join-Path $Dst "tests")
Copy-Item -Recurse (Join-Path $Root "contracts") (Join-Path $Dst "contracts")
Remove-Item -Force (Join-Path $Dst "tests\run.sh"), (Join-Path $Dst "tests\run.ps1") -ErrorAction SilentlyContinue
Set-Location (Join-Path $Root "vendor\derohe")
go vet -mod=vendor ./veil/...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go test -mod=vendor -count=1 ./veil/... @args
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
