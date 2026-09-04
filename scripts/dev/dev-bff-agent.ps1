# ============================================================================
# 【开发模式】一键启动 PrivShield 引擎 + Go BFF + Vite 控制台 (PowerShell)
# Launch PrivShield Agent + Go BFF + Vite Dev UI in DEV mode (Windows PowerShell)
#
# 用法 / Usage:
#   .\scripts\dev\dev-bff-agent.ps1 [-Mtls] [-Force]
# ============================================================================

param (
    [switch]$Mtls,
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Resolve-Path "$ScriptDir\..\.."
$ConsoleDir = "$ProjectRoot\console"
$PidsDir = "$ProjectRoot\.pids"
$LogsDir = "$ProjectRoot\.logs"

New-Item -ItemType Directory -Force -Path $PidsDir, $LogsDir | Out-Null

$AgentVenv = "$ProjectRoot\.venv"
$CertDir = "$ConsoleDir\bff-go\certs"

$ConsoleUrl = "http://127.0.0.1:8081"
$AgentUrl = if ($Mtls) { "https://127.0.0.1:8079" } else { "http://127.0.0.1:8079" }
$AgentGrpcAddr = "127.0.0.1:50051"
$ViteUrl = "http://localhost:5173"

Write-Host "编译 Go gRPC 代理后端..."
Push-Location "$ConsoleDir\bff-go"
go build -o bin\backend-go.exe .\cmd\server
Pop-Location

Write-Host "启动 PrivShield Agent..."
Push-Location $ProjectRoot
if ($Mtls) {
    $env:AGENT_TLS_ENABLED = "true"
    $env:AGENT_TLS_CERT_FILE = "$CertDir\server.crt"
    $env:AGENT_TLS_KEY_FILE = "$CertDir\server.key"
    $env:AGENT_TLS_CA_FILE = "$CertDir\ca.crt"
    $env:AGENT_AUTH_INTERNAL_MTLS_ENABLED = "true"
    $env:AGENT_AUTH_MTLS_ALLOWED_CNS = '["privshield-client"]'
}
$agentProcess = Start-Process -FilePath "go" -ArgumentList "run ./engine-go/cmd/privshield-agent" -NoNewWindow -PassThru
Pop-Location

Start-Sleep -Seconds 2

Write-Host "启动 Go gRPC 代理后端..."
Push-Location "$ConsoleDir\bff-go"
if ($Mtls) {
    $env:PRIVACY_AGENT_TLS_ENABLED = "true"
    $env:PRIVACY_AGENT_TLS_CA_FILE = "$CertDir\ca.crt"
    $env:PRIVACY_AGENT_TLS_CERT_FILE = "$CertDir\client.crt"
    $env:PRIVACY_AGENT_TLS_KEY_FILE = "$CertDir\client.key"
    $env:PRIVACY_AGENT_TLS_SERVER_NAME = "localhost"
}
$goProcess = Start-Process -FilePath ".\bin\backend-go.exe" -NoNewWindow -PassThru
Pop-Location

Start-Sleep -Seconds 1

Write-Host "启动 Vite 前端开发服务器..."
Push-Location "$ConsoleDir\web"
$env:VITE_PROXY_TARGET = $ConsoleUrl
$viteProcess = Start-Process -FilePath "pnpm" -ArgumentList "dev" -NoNewWindow -PassThru
Pop-Location

Write-Host ""
Write-Host "================================================================="
Write-Host "🎉 PrivShield 全量服务 (Agent + Go BFF + Vite UI) 已全部启动！"
Write-Host "  UI:   $ViteUrl"
Write-Host "  BFF:  $ConsoleUrl"
Write-Host "  Agent:$AgentUrl"
Write-Host "================================================================="

try {
    $agentProcess.WaitForExit()
} finally {
    Stop-Process -Id $agentProcess.Id -ErrorAction SilentlyContinue
    Stop-Process -Id $goProcess.Id -ErrorAction SilentlyContinue
    Stop-Process -Id $viteProcess.Id -ErrorAction SilentlyContinue
}
