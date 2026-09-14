# restart-server.ps1 - restart infra-ops server (127.0.0.1:8090)
# Usage:  powershell -ExecutionPolicy Bypass -File script\restart-server.ps1

$dir = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$exe = Join-Path $dir 'infra-ops-server.exe'

if (-not (Test-Path $exe)) { Write-Host "ERROR: $exe not found"; exit 1 }

# 1. stop whatever holds port 8090
$conn = Get-NetTCPConnection -State Listen -LocalPort 8090 -ErrorAction SilentlyContinue | Select-Object -First 1
if ($conn) {
    Write-Host "stopping old process PID=$($conn.OwningProcess)"
    Stop-Process -Id $conn.OwningProcess -Force
    Start-Sleep -Milliseconds 1000
} else {
    Write-Host "port 8090 free"
}

# 2. start detached (survives terminal close)
Start-Process -FilePath $exe -WorkingDirectory $dir -WindowStyle Minimized

# 3. verify
Start-Sleep -Seconds 3
$c = Get-NetTCPConnection -State Listen -LocalPort 8090 -ErrorAction SilentlyContinue | Select-Object -First 1
if ($c) {
    Write-Host "OK: infra-ops running, PID=$($c.OwningProcess), http://127.0.0.1:8090"
} else {
    Write-Host "FAILED: not listening. Run the exe in a console to see the error."
    exit 1
}
