# Sets up this Windows machine to build and deploy with Claude Code on the
# Company Deployment Platform. Download it from the platform and run it:
#
#   irm __PLATFORM_URL__/downloads/install.ps1 | iex
#
# or save it first and run `powershell -ExecutionPolicy Bypass -File install.ps1`.
# Safe to run again: it upgrades in place.
#
# What it does, all under your own user profile (no administrator rights):
#   1. a Python virtual environment in ~\.company-deploy\venv with the
#      Company Deployment MCP server and the `company-deploy` command
#   2. the Company Deployment skill in ~\.claude\skills\company-deployment
#   3. registers the MCP server with Claude Code (user scope)
#   4. signs this machine in: you approve a code in the Admin Portal, and
#      the machine stores its own API key. The key is never shown.
param(
    [string]$PlatformUrl = "__PLATFORM_URL__",
    [switch]$SkipLogin
)
$ErrorActionPreference = "Stop"
$PlatformUrl = $PlatformUrl.TrimEnd("/")
$Api = "$PlatformUrl/api"
$Downloads = "$PlatformUrl/downloads"
$Wheel = "__WHEEL__"
$Root = Join-Path $env:USERPROFILE ".company-deploy"
$Venv = Join-Path $Root "venv"
$SkillDir = Join-Path $env:USERPROFILE ".claude\skills\company-deployment"

function Step($text) { Write-Host "==> $text" -ForegroundColor Cyan }

# Runs a program and returns its exit code. Windows PowerShell 5.1 turns
# anything a native program writes to stderr into a terminating error
# under ErrorActionPreference=Stop — even a harmless notice, or `claude mcp
# remove` saying there was nothing to remove. Success is judged by the exit
# code instead, and the program's output is shown as-is.
function Invoke-Native([string]$Exe, [string[]]$Arguments) {
    $previous = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        & $Exe @Arguments 2>&1 | ForEach-Object { Write-Host "    $_" }
        return $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previous
    }
}

Step "Checking the platform at $PlatformUrl"
try {
    $null = Invoke-RestMethod "$Api/healthz" -TimeoutSec 10
} catch {
    throw "Can't reach the platform at $Api ($($_.Exception.Message)). Check the address and that you're on the company network."
}

Step "Finding Python 3.11 or newer"
$Python = $null
foreach ($candidate in @(@("py", "-3"), @("python"), @("python3"))) {
    $exe = $candidate[0]
    if (-not (Get-Command $exe -ErrorAction SilentlyContinue)) { continue }
    $probeArgs = @($candidate | Select-Object -Skip 1) + @("-c", "import sys; print('%d.%d' % sys.version_info[:2])")
    try { $version = (& $exe @probeArgs 2>$null | Select-Object -Last 1) } catch { continue }
    if ($version -and [version]$version -ge [version]"3.11") { $Python = $candidate; break }
}
if (-not $Python) { throw "Python 3.11 or newer is required: install it from https://www.python.org/downloads/ and run this again." }

Step "Installing the Company Deployment MCP server into $Venv"
New-Item -ItemType Directory -Force -Path $Root | Out-Null
if (-not (Test-Path (Join-Path $Venv "Scripts\python.exe"))) {
    $pyArgs = @($Python | Select-Object -Skip 1) + @("-m", "venv", $Venv)
    if ((Invoke-Native $Python[0] $pyArgs) -ne 0) { throw "Creating the virtual environment failed." }
}
$VenvPython = Join-Path $Venv "Scripts\python.exe"
$WheelPath = Join-Path $Root $Wheel
Invoke-WebRequest "$Downloads/$Wheel" -OutFile $WheelPath -UseBasicParsing
if ((Invoke-Native $VenvPython @("-m", "pip", "install", "--quiet", "--disable-pip-version-check", "--upgrade", $WheelPath)) -ne 0) {
    throw "Installing the MCP server failed (its dependencies come from PyPI or your pip mirror)."
}
# The wheel's version doesn't change between platform updates, so on a
# re-run pip calls it "already installed" and keeps the old code. Replace
# the package itself every time; its dependencies stay as installed above.
if ((Invoke-Native $VenvPython @("-m", "pip", "install", "--quiet", "--disable-pip-version-check", "--force-reinstall", "--no-deps", $WheelPath)) -ne 0) {
    throw "Updating the MCP server failed."
}
Remove-Item $WheelPath -Force

Step "Installing the Company Deployment skill into $SkillDir"
$Zip = Join-Path $Root "company-deployment-skill.zip"
Invoke-WebRequest "$Downloads/company-deployment-skill.zip" -OutFile $Zip -UseBasicParsing
if (Test-Path $SkillDir) { Remove-Item $SkillDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path $SkillDir | Out-Null
Expand-Archive -Path $Zip -DestinationPath $SkillDir -Force
Remove-Item $Zip -Force

Step "Registering the MCP server with Claude Code"
if (Get-Command claude -ErrorAction SilentlyContinue) {
    # Replaces an earlier registration; failing because there was none is fine.
    $null = Invoke-Native "claude" @("mcp", "remove", "company-deployment", "-s", "user")
    if ((Invoke-Native "claude" @("mcp", "add", "company-deployment", "-s", "user", "-e", "PLATFORM_API_BASE_URL=$Api", "--", $VenvPython, "-m", "mcp_server.server")) -ne 0) {
        throw "claude mcp add failed."
    }
} else {
    Write-Warning "Claude Code (the 'claude' command) isn't installed or not on PATH. Once it is, run:"
    Write-Host "  claude mcp add company-deployment -s user -e PLATFORM_API_BASE_URL=$Api -- `"$VenvPython`" -m mcp_server.server"
}

$CompanyDeploy = Join-Path $Venv "Scripts\company-deploy.exe"
if ($SkipLogin) {
    Step "Skipping sign-in. Later, run: & `"$CompanyDeploy`" login --url $Api"
} else {
    Step "Signing this machine in"
    if ((Invoke-Native $CompanyDeploy @("login", "--url", $Api)) -ne 0) {
        throw "Sign-in didn't complete. Run it again with: & `"$CompanyDeploy`" login --url $Api"
    }
}

Write-Host ""
Write-Host "Done. Open Claude Code in your project and ask it to deploy - it uses the company-deployment skill." -ForegroundColor Green
Write-Host "Admin Portal: $PlatformUrl"
