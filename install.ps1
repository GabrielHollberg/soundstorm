# SoundStorm installer for Windows.
#
#   irm https://raw.githubusercontent.com/gabehollberg/soundstorm/main/install.ps1 | iex
#
# It downloads one compose file, picks a free port, starts the stack and waits
# until it answers. Everything it needs is Docker Desktop; everything it leaves
# behind is a folder you can delete.

#Requires -Version 5.1
$ErrorActionPreference = 'Stop'

$Repo       = if ($env:SOUNDSTORM_REPO) { $env:SOUNDSTORM_REPO } else { 'gabehollberg/soundstorm' }
$Branch     = if ($env:SOUNDSTORM_BRANCH) { $env:SOUNDSTORM_BRANCH } else { 'main' }
$ComposeUrl = if ($env:SOUNDSTORM_COMPOSE_URL) { $env:SOUNDSTORM_COMPOSE_URL }
              else { "https://raw.githubusercontent.com/$Repo/$Branch/docker-compose.yml" }
$Dir        = if ($env:SOUNDSTORM_DIR) { $env:SOUNDSTORM_DIR } else { Join-Path $PWD 'soundstorm' }
$FirstPort  = if ($env:SOUNDSTORM_PORT) { [int]$env:SOUNDSTORM_PORT } else { 8099 }

function Step($text) { Write-Host "==> " -ForegroundColor White -NoNewline; Write-Host $text }
function Note($text) { Write-Host "    $text" -ForegroundColor DarkGray }

# Stop says why it stopped and what to do about it. An installer that reports
# "error: 1" has failed twice.
function Stop-With($text) {
    Write-Host ""
    Write-Host "SoundStorm could not start." -ForegroundColor Red
    Write-Host ""
    Write-Host $text
    Write-Host ""
    exit 1
}

function Test-Docker {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Stop-With @"
Docker Desktop is not installed.

Docker runs the media servers SoundStorm sits on top of, so it is the one thing
you have to install yourself. It is free for personal use.

  https://www.docker.com/products/docker-desktop/

Install it, start it, then run this again.
"@
    }

    # Installed is not running, and this is the single most common failure:
    # somebody installs Docker Desktop, never opens it, and gets a wall of pipe
    # errors that say nothing about which application to launch.
    docker info *> $null
    if ($LASTEXITCODE -ne 0) {
        Stop-With @"
Docker Desktop is installed but not running.

Open Docker Desktop from the Start menu and wait until it says Running, then
run this again. It can take a minute on a cold start.
"@
    }
}

function Get-ComposeCommand {
    docker compose version *> $null
    if ($LASTEXITCODE -eq 0) { return @('docker', 'compose') }
    if (Get-Command docker-compose -ErrorAction SilentlyContinue) { return @('docker-compose') }
    Stop-With "Docker is running but Docker Compose is missing. Reinstall Docker Desktop, which includes it."
}

function Invoke-Compose {
    param([string[]]$Arguments)
    & $script:Compose[0] @($script:Compose[1..($script:Compose.Count - 1)] + $Arguments)
    return $LASTEXITCODE
}

# Test-PortFree binds the port rather than listing connections: a listener with
# no connection to it does not show up in Get-NetTCPConnection on every
# Windows build, and binding is the question we actually care about.
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

Write-Host ""
Write-Host "SoundStorm" -ForegroundColor White -NoNewline
Write-Host " - one login and one search box over your media library"
Write-Host ""

Step "Checking Docker"
Test-Docker
$script:Compose = Get-ComposeCommand
Note (docker --version)

Step "Setting up $Dir"

# The compose project name is fixed, so a second install in a second folder
# does not get its own stack - it adopts the first one, ends up pointing at a
# library folder nobody put anything in, and looks broken for no visible
# reason. Compose records the directory it was launched from, so we can ask.
$previous = (docker inspect soundstorm --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' 2>$null)
if ($LASTEXITCODE -ne 0) { $previous = '' }
if ($previous -and $previous -ne $Dir -and -not (Test-Path (Join-Path $Dir 'docker-compose.yml'))
    -and $env:SOUNDSTORM_FORCE -ne '1') {
    Stop-With @"
SoundStorm is already installed in another folder:

  $previous

Installing it here as well would not give you a second copy - both folders
would drive the same containers, and this one would point at an empty library.

To use the existing install:   cd "$previous"
To move it here instead:       cd "$previous"; docker compose down, then run this again
To install anyway:             `$env:SOUNDSTORM_FORCE=1; ./install.ps1
"@
}

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
Set-Location $Dir

$upgrade = $false
if ((Test-Path docker-compose.yml) -and $env:SOUNDSTORM_FORCE -ne '1') {
    Note "already installed here - upgrading it instead"
    $upgrade = $true
} else {
    try {
        # Download to a temporary name so a failure cannot leave a working
        # install with half a compose file in it.
        Invoke-WebRequest -Uri $ComposeUrl -OutFile 'docker-compose.yml.new' -UseBasicParsing
        Move-Item -Force 'docker-compose.yml.new' 'docker-compose.yml'
    } catch {
        Stop-With "Could not download the compose file from`n`n  $ComposeUrl`n`nCheck your connection and try again."
    }
    Note "downloaded docker-compose.yml"
}

foreach ($folder in 'music', 'movies', 'tv', 'audiobooks', 'ebooks') {
    New-Item -ItemType Directory -Force -Path (Join-Path 'library' $folder) | Out-Null
}

if (-not $upgrade) {
    Step "Choosing a port"
    $port = $FirstPort
    while (-not (Test-PortFree $port)) {
        $port++
        if ($port -gt $FirstPort + 20) {
            Stop-With "Ports $FirstPort to $port are all in use. Pick one yourself:`n`n  `$env:SOUNDSTORM_PORT=9000; ./install.ps1"
        }
    }
    if ($port -ne $FirstPort) { Note "$FirstPort was busy, using $port" } else { Note "using $port" }
    # Compose reads .env from beside the compose file, so the choice sticks for
    # every later `docker compose up` without anyone having to remember it.
    "SOUNDSTORM_PORT=$port" | Out-File -FilePath '.env' -Encoding ascii
} else {
    $line = Select-String -Path '.env' -Pattern '^SOUNDSTORM_PORT=(\d+)' -ErrorAction SilentlyContinue
    $port = if ($line) { [int]$line.Matches[0].Groups[1].Value } else { $FirstPort }
}

if ($upgrade) {
    Step "Checking for newer versions"
} else {
    Step "Downloading the media servers"
    Note "about 3GB the first time - Jellyfin is most of it"
}
if ((Invoke-Compose @('pull')) -ne 0) {
    Stop-With "Could not download the images. That is almost always the network.`nCheck your connection and run this again - anything already downloaded is kept."
}

Step "Starting"
# Captured rather than streamed, so a failure can be read and explained instead
# of leaving somebody to interpret a Docker error.
$out = & $script:Compose[0] @($script:Compose[1..($script:Compose.Count - 1)] + @('up', '-d')) 2>&1
if ($LASTEXITCODE -ne 0) {
    $out | ForEach-Object { Write-Host $_ }
    if ($out -match 'already allocated|address already in use|forbidden by its access permissions') {
        Stop-With "Port $port is already being used by something else.`n`nPick another one and run this again:`n`n  `$env:SOUNDSTORM_PORT=9000; ./install.ps1"
    }
    Stop-With "The containers would not start. This usually says why:`n`n  cd $Dir; docker compose logs"
}

Step "Waiting for SoundStorm to answer"
$url = "http://localhost:$port"
$waited = 0
while ($true) {
    try {
        Invoke-WebRequest -Uri "$url/healthz" -UseBasicParsing -TimeoutSec 5 | Out-Null
        break
    } catch {
        $waited += 2
        if ($waited -gt 120) {
            Stop-With "SoundStorm started but never answered on $url.`n`n  cd $Dir; docker compose logs soundstorm"
        }
        Start-Sleep -Seconds 2
    }
}

Write-Host ""
if ($upgrade) {
    Write-Host "Up to date." -ForegroundColor Green -NoNewline
    Write-Host " SoundStorm is running at $url."
} else {
    Write-Host "Ready." -ForegroundColor Green -NoNewline
    Write-Host " Open $url and create your account."
}
Write-Host ""
Write-Host "Your media goes in $Dir\library :"
Write-Host "    music\  movies\  tv\  audiobooks\  ebooks\"
Write-Host ""
Note "The media servers are still setting themselves up in the background."
Note "The app shows you when each one is ready - that takes a minute or two."
Write-Host ""
Note "stop:    cd $Dir; docker compose down"
Note "logs:    cd $Dir; docker compose logs -f"
Note "upgrade: run this installer again"
Write-Host ""

Start-Process $url
