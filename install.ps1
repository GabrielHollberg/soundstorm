# SoundStorm installer for Windows.
#
# Double-click SoundStorm-Setup.cmd, or from PowerShell:
#
#   irm https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/install.ps1 | iex
#
# It is written for somebody who has never opened a terminal. That means it
# installs Docker Desktop itself rather than sending them to a website, starts
# it rather than telling them to, and leaves a Start Menu shortcut rather than
# an address to remember. Every question it cannot answer becomes an
# instruction, not an error code.
#
#   -Launch        start an existing install and open it (what the shortcut runs)
#   -NoShortcuts   skip the Start Menu, Desktop and startup shortcuts
#   -NoAutoStart   install, but do not start with Windows

#Requires -Version 5.1
[CmdletBinding()]
param(
    [switch]$Launch,
    [switch]$NoShortcuts,
    [switch]$NoAutoStart,
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Stop'

$Repo       = if ($env:SOUNDSTORM_REPO) { $env:SOUNDSTORM_REPO } else { 'GabrielHollberg/soundstorm' }
$Branch     = if ($env:SOUNDSTORM_BRANCH) { $env:SOUNDSTORM_BRANCH } else { 'main' }
$RawBase    = "https://raw.githubusercontent.com/$Repo/$Branch"
$ComposeUrl = if ($env:SOUNDSTORM_COMPOSE_URL) { $env:SOUNDSTORM_COMPOSE_URL } else { "$RawBase/docker-compose.yml" }
$ScriptUrl  = if ($env:SOUNDSTORM_SCRIPT_URL) { $env:SOUNDSTORM_SCRIPT_URL } else { "$RawBase/install.ps1" }

# Under the user's own folder rather than Program Files: the media library
# lives beside the compose file, and it has to be somewhere they can drop a
# hard drive of music into without a permission prompt.
$Dir       = if ($env:SOUNDSTORM_DIR) { $env:SOUNDSTORM_DIR } else { Join-Path $env:USERPROFILE 'SoundStorm' }
$FirstPort = if ($env:SOUNDSTORM_PORT) { [int]$env:SOUNDSTORM_PORT } else { 8099 }

function Step($text) { Write-Host ""; Write-Host "  $text" -ForegroundColor White }
function Note($text) { Write-Host "    $text" -ForegroundColor DarkGray }
function Good($text) { Write-Host "    $text" -ForegroundColor Green }

# Stop says why it stopped and what to do about it. An installer that reports
# "error: 1" has failed twice.
#
# In -Launch mode it also puts the message in a dialog box. That path runs from
# a desktop shortcut with a minimised window, so console text is written where
# nobody will ever see it - the failure just looks like clicking the icon did
# nothing at all.
function Stop-With($text) {
    Write-Host ""
    Write-Host "  SoundStorm could not finish." -ForegroundColor Red
    Write-Host ""
    Write-Host $text
    Write-Host ""
    if ($Launch) { Show-Problem $text }
    exit 1
}

function Show-Problem($text) {
    try {
        $shell = New-Object -ComObject WScript.Shell
        # 120 seconds rather than 0: at startup there may be nobody to click
        # it, and a modal box waiting forever would keep the process alive.
        # 48 is the warning icon.
        $shell.Popup($text, 120, 'SoundStorm', 48) | Out-Null
    } catch {
        # A dialog is a nicety; failing to show one must not become the error.
    }
}

# Invoke-DockerBounded runs docker with a deadline.
#
# `compose up -d` normally takes seconds, but it will sit for a very long time
# trying to reach a registry it cannot. From a minimised shortcut that is
# indistinguishable from the icon doing nothing, so the launcher gives it a
# limit and reports rather than waiting.
function Invoke-DockerBounded {
    param([string[]]$Arguments, [int]$TimeoutSeconds = 120)

    $process = Start-Process -FilePath 'docker' -ArgumentList $Arguments `
        -NoNewWindow -PassThru
    # Reading .Handle is not a no-op and is not optional. Start-Process
    # -PassThru hands back a Process object with no cached handle, and without
    # one WaitForExit(timeout) never observes the exit - it returns false at
    # the deadline for a program that finished in a second. The symptom is
    # every launch taking exactly as long as the timeout and then reporting
    # failure, with the containers running perfectly well behind it.
    $null = $process.Handle
    if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
        try { $process.Kill() } catch {}
        return 1
    }
    return $process.ExitCode
}

# Invoke-Docker runs docker with stderr made harmless.
#
# PowerShell 5.1 wraps every stderr line from a native program in an
# ErrorRecord, and with $ErrorActionPreference = 'Stop' the first one throws.
# docker compose writes its ordinary progress to stderr, so `compose up` failed
# this script by succeeding noisily. Anything that shells out goes through here.
function Invoke-Docker {
    param([string[]]$Arguments, [switch]$Capture)

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        if ($Capture) {
            $lines = & docker @Arguments 2>&1 | ForEach-Object { "$_" }
            return [pscustomobject]@{
                ExitCode = $LASTEXITCODE
                Output   = ($lines -join [Environment]::NewLine)
            }
        }
        # Piped through Write-Host rather than run bare: without this the
        # stderr lines still arrive as ErrorRecords and print as a red
        # NativeCommandError block, which looks like a crash to anybody who
        # has not seen one before. docker compose reports its progress there.
        & docker @Arguments 2>&1 | ForEach-Object { Write-Host "$_" }
        return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = '' }
    } finally {
        $ErrorActionPreference = $previousPreference
    }
}

function Test-DockerRunning {
    docker info *> $null
    return ($LASTEXITCODE -eq 0)
}

function Get-DockerDesktopPath {
    foreach ($candidate in @(
        (Join-Path $env:ProgramFiles 'Docker\Docker\Docker Desktop.exe'),
        (Join-Path ${env:ProgramFiles(x86)} 'Docker\Docker\Docker Desktop.exe')
    )) {
        if ($candidate -and (Test-Path $candidate)) { return $candidate }
    }
    return $null
}

# Install-Docker uses winget, which ships with Windows 10 1809 and later.
#
# The alternative is telling somebody to visit a website, pick the right
# download and run an installer, which is the single step this script exists
# to remove.
function Install-Docker {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        Stop-With @"
  SoundStorm needs Docker Desktop, and this PC does not have the installer
  tool (winget) that would fetch it automatically.

  Install Docker Desktop from here, then run this again:

    https://www.docker.com/products/docker-desktop/
"@
    }

    Note "Docker Desktop is not installed. Getting it now."
    Note "This is a big download and takes a few minutes."
    winget install --exact --id Docker.DockerDesktop --accept-source-agreements --accept-package-agreements --silent
    # 0 is installed; -1978335189 is "already installed", which is not a
    # failure however it reads.
    if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne -1978335189) {
        Stop-With @"
  Docker Desktop would not install automatically.

  Install it by hand from here, then run this again:

    https://www.docker.com/products/docker-desktop/
"@
    }
    Good "Docker Desktop installed."

    # winget does not refresh this session's PATH.
    $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
                [Environment]::GetEnvironmentVariable('Path', 'User')
}

# Start-Docker launches Docker Desktop and waits for its engine.
#
# "Docker is installed but not running" is the most common failure on Windows
# by a distance, and the old answer - go and open it yourself - is exactly the
# kind of instruction this is trying not to give.
function Start-Docker {
    $exe = Get-DockerDesktopPath
    if (-not $exe) {
        Stop-With @"
  Docker Desktop is installed but this script cannot find it to start it.

  Open Docker Desktop from the Start menu, wait until it says Running, then
  run this again.
"@
    }

    Note "Starting Docker Desktop. This takes a minute on a cold start."
    Start-Process -FilePath $exe | Out-Null

    $waited = 0
    while (-not (Test-DockerRunning)) {
        Start-Sleep -Seconds 3
        $waited += 3
        if ($waited % 30 -eq 0) { Note "still starting... ($waited seconds)" }
        if ($waited -gt 300) {
            Stop-With @"
  Docker Desktop was started but its engine never came up.

  Open Docker Desktop from the Start menu and see what it says - the first
  run sometimes asks a question or wants a restart. Then run this again.
"@
        }
    }
    Good "Docker is running."
}

function Initialize-Docker {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Install-Docker
    }
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Stop-With @"
  Docker Desktop was installed but is not on this window's PATH yet.

  Close this window, open the setup again, and it should find it. If not,
  restart the PC first - Docker usually asks for one anyway.
"@
    }
    if (-not (Test-DockerRunning)) { Start-Docker }
}

# Test-PortFree binds the port rather than listing connections: a listener with
# no connection to it does not show up in Get-NetTCPConnection on every Windows
# build, and binding is the question we actually care about.
# Get-ExistingInstallPath reads where an existing install was launched from.
#
# Through ConvertFrom-Json rather than a --format template, because PowerShell
# strips the inner double quotes out of
# '{{index .Config.Labels "com.docker.compose..."}}' on the way to docker, and
# docker then fails with `function "com" not defined`. That is invisible until
# the script is actually run on Windows.
function Get-ExistingInstallPath {
    $found = ''
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $raw = docker inspect soundstorm 2>$null
        if ($LASTEXITCODE -eq 0 -and $raw) {
            $labels = ($raw | ConvertFrom-Json)[0].Config.Labels
            if ($labels) {
                $found = $labels.'com.docker.compose.project.working_dir'
            }
        }
    } catch {
        $found = ''
    } finally {
        $ErrorActionPreference = $previousPreference
    }
    if (-not $found) { return '' }
    return $found
}

function Test-PortFree([int]$Port) {
    $listener = $null
    try {
        $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $Port)
        $listener.Start()
        return $true
    } catch {
        return $false
    } finally {
        if ($listener) { try { $listener.Stop() } catch {} }
    }
}

function Wait-ForSoundStorm([string]$Url) {
    $waited = 0
    while ($true) {
        try {
            Invoke-WebRequest -Uri "$Url/healthz" -UseBasicParsing -TimeoutSec 5 | Out-Null
            return
        } catch {
            Start-Sleep -Seconds 2
            $waited += 2
            if ($waited -gt 180) {
                Stop-With "  SoundStorm started but never answered on $Url.`n`n  Show this to whoever gave you the app:`n`n    cd `"$Dir`"; docker compose logs soundstorm"
            }
        }
    }
}

function Get-InstalledPort {
    $envFile = Join-Path $Dir '.env'
    if (Test-Path $envFile) {
        $line = Select-String -Path $envFile -Pattern '^SOUNDSTORM_PORT=(\d+)' -ErrorAction SilentlyContinue
        if ($line) { return [int]$line.Matches[0].Groups[1].Value }
    }
    return $FirstPort
}

# New-Shortcut writes a .lnk. WScript.Shell is the only way to do that without
# shipping a compiled helper, and it is on every Windows since XP.
function New-Shortcut($Path, $Target, $Arguments, $WorkingDirectory, $Description, $Minimised) {
    $shell = New-Object -ComObject WScript.Shell
    $link = $shell.CreateShortcut($Path)
    $link.TargetPath = $Target
    if ($Arguments) { $link.Arguments = $Arguments }
    if ($WorkingDirectory) { $link.WorkingDirectory = $WorkingDirectory }
    $link.Description = $Description
    # 7 is minimised: the launcher makes sure Docker is up before opening a
    # browser, and that is not work anybody wants to watch.
    if ($Minimised) { $link.WindowStyle = 7 }
    $link.Save()
}

function Install-Shortcuts {
    $localScript = Join-Path $Dir 'soundstorm.ps1'
    $powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$localScript`" -Launch"

    $startMenu = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
    New-Shortcut (Join-Path $startMenu 'SoundStorm.lnk') $powershell $arguments $Dir `
        'Open your media library' $true
    New-Shortcut (Join-Path ([Environment]::GetFolderPath('Desktop')) 'SoundStorm.lnk') `
        $powershell $arguments $Dir 'Open your media library' $true

    # Somewhere to put files, one click away. The app takes a drag-and-drop
    # too, but a folder is what people reach for with a hard drive of music.
    New-Shortcut (Join-Path ([Environment]::GetFolderPath('Desktop')) 'SoundStorm media.lnk') `
        (Join-Path $Dir 'library') $null $null 'Put your music, films and books in here' $false

    if (-not $NoAutoStart) {
        $startup = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Startup'
        New-Shortcut (Join-Path $startup 'SoundStorm.lnk') $powershell `
            "-NoProfile -ExecutionPolicy Bypass -File `"$localScript`" -Launch -NoBrowser" $Dir `
            'Start SoundStorm with Windows' $true
    }
    Good "Added SoundStorm to the Start menu and the desktop."
}

# --- opening an install that is already here ----------------------------------

if ($Launch) {
    if (-not (Test-Path (Join-Path $Dir 'docker-compose.yml'))) {
        Stop-With "  SoundStorm is not installed in $Dir. Run the setup again."
    }
    Set-Location $Dir
    Initialize-Docker
    if ((Invoke-DockerBounded @('compose', 'up', '-d')) -ne 0) {
        Stop-With "  SoundStorm would not start.`n`n  Try turning the PC off and on again. If it keeps happening, show`n  this to whoever gave you the app:`n`n    cd `"$Dir`"; docker compose logs"
    }
    $port = Get-InstalledPort
    $url = "http://localhost:$port"
    Wait-ForSoundStorm $url
    # At startup there is nobody watching yet, so the browser stays shut; the
    # desktop icon is what opens it.
    if (-not $NoBrowser) { Start-Process $url }
    exit 0
}

# --- installing -----------------------------------------------------------------

Write-Host ""
Write-Host "  SoundStorm" -ForegroundColor White -NoNewline
Write-Host " - all your music, films, books and audiobooks in one place"
Write-Host "  -----------------------------------------------------------"

Step "Checking for Docker"
Initialize-Docker
Note (docker --version)

Step "Setting up $Dir"

# The compose project name is fixed, so a second install in a second folder
# adopts the first one's containers and then points at an empty library.
$previous = Get-ExistingInstallPath
# Split out rather than written as one long condition: PowerShell 5.1 will not
# take a line break before an operator inside an if, and the one-line version
# is unreadable.
$installedHere = Test-Path (Join-Path $Dir 'docker-compose.yml')
$elsewhere = $previous -and ($previous -ne $Dir) -and (-not $installedHere)
if ($elsewhere -and $env:SOUNDSTORM_FORCE -ne '1') {
    Stop-With @"
  SoundStorm is already installed in another folder:

    $previous

  Installing it here as well would not give you a second copy - both folders
  would drive the same containers, and this one would point at an empty
  library. Use the one that is already there, or remove it first.
"@
}

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
Set-Location $Dir

$upgrade = (Test-Path 'docker-compose.yml') -and $env:SOUNDSTORM_FORCE -ne '1'
if ($upgrade) {
    Note "Already installed here - updating it instead."
} else {
    try {
        # To a temporary name first, so a failed download cannot leave a
        # working install with half a compose file in it.
        Invoke-WebRequest -Uri $ComposeUrl -OutFile 'docker-compose.yml.new' -UseBasicParsing
        Move-Item -Force 'docker-compose.yml.new' 'docker-compose.yml'
    } catch {
        Stop-With "  Could not download SoundStorm from`n`n    $ComposeUrl`n`n  Check the internet connection and try again."
    }
}

# A copy of this script lives beside the install, so the desktop shortcut has
# something to run and updating later needs no web address.
try {
    Invoke-WebRequest -Uri $ScriptUrl -OutFile 'soundstorm.ps1' -UseBasicParsing
} catch {
    if ($PSCommandPath -and (Test-Path $PSCommandPath)) {
        Copy-Item $PSCommandPath 'soundstorm.ps1' -Force
    }
}

foreach ($folder in 'music', 'movies', 'tv', 'audiobooks', 'ebooks') {
    New-Item -ItemType Directory -Force -Path (Join-Path 'library' $folder) | Out-Null
}

if ($upgrade) {
    $port = Get-InstalledPort
} else {
    $port = $FirstPort
    while (-not (Test-PortFree $port)) {
        $port++
        if ($port -gt $FirstPort + 20) {
            Stop-With "  Ports $FirstPort to $port are all in use on this PC.`n`n  Show this to whoever gave you the app."
        }
    }
    if ($port -ne $FirstPort) { Note "Port $FirstPort was busy, using $port." }
    # Compose reads .env from beside the compose file, so the choice sticks.
    "SOUNDSTORM_PORT=$port" | Out-File -FilePath '.env' -Encoding ascii
}

if ($upgrade) {
    Step "Checking for a newer version"
} else {
    Step "Downloading the media servers"
    Note "About 3GB the first time. This is the long part - leave it running."
}
# Shown rather than captured: this is the part that takes minutes, and a
# silent window is how somebody decides it has hung.
$pull = Invoke-Docker @('compose', 'pull')
if ($pull.ExitCode -ne 0) {
    Stop-With "  Could not download the media servers. That is almost always the`n  internet connection. Try again - anything already downloaded is kept."
}

Step "Starting SoundStorm"
$start = Invoke-Docker @('compose', 'up', '-d') -Capture
if ($start.ExitCode -ne 0) {
    Write-Host $start.Output
    if ($start.Output -match 'already allocated|address already in use|forbidden by its access permissions') {
        Stop-With "  Port $port is already being used by another program on this PC.`n`n  Show this to whoever gave you the app."
    }
    Stop-With "  SoundStorm would not start.`n`n  Show this to whoever gave you the app:`n`n    cd `"$Dir`"; docker compose logs"
}

$url = "http://localhost:$port"
Wait-ForSoundStorm $url

if (-not $NoShortcuts) {
    Step "Adding shortcuts"
    try {
        Install-Shortcuts
    } catch {
        # Not worth failing an otherwise finished install over.
        Note "Could not add shortcuts: $($_.Exception.Message)"
        Note "SoundStorm still works at $url"
    }
}

Write-Host ""
Write-Host "  -----------------------------------------------------------"
if ($upgrade) {
    Write-Host "  Up to date." -ForegroundColor Green -NoNewline
    Write-Host " SoundStorm is running."
} else {
    Write-Host "  Done." -ForegroundColor Green -NoNewline
    Write-Host " SoundStorm is running at $url"
}
Write-Host ""
Write-Host "  Opening it now. Pick a username and password on the first screen -"
Write-Host "  that is your account, and nobody else can create one."
Write-Host ""
Write-Host "  To add music, films or books: drag them onto the window, or put"
Write-Host "  them in the 'SoundStorm media' folder on your desktop."
Write-Host ""
if (-not $NoShortcuts) {
    Write-Host "  Next time, click the SoundStorm icon on your desktop." -ForegroundColor DarkGray
    if (-not $NoAutoStart) {
        Write-Host "  It also starts by itself when you turn the PC on." -ForegroundColor DarkGray
    }
}
Write-Host ""

Start-Process $url
