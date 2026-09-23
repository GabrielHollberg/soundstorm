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
#   -Uninstall     remove SoundStorm, keeping the media library
#   -Https         real https for a soundstorm.dev name (the default already)
#   -NoHttps       plain http only
#   -Tailscale     also reach it away from home, over a tailnet
#   -NoTailscale   stop doing that
#   -NoShortcuts   skip the Start Menu, Desktop and startup shortcuts
#   -NoAutoStart   install, but do not start with Windows
#   -Library PATH  keep the media library somewhere else - an external drive
#
# Updating is the same as installing: run it again. It pulls newer images and
# restarts, and leaves everything else alone. -Https and -NoHttps work on an
# existing install for the same reason - they only change one line of .env.
#
# The double-dash spellings (--https) bind too, which is what somebody arriving
# from the Linux instructions will type.

#Requires -Version 5.1
[CmdletBinding()]
param(
    [switch]$Launch,
    [switch]$Uninstall,
    [switch]$Https,
    # No alias here, unlike -NoTailscale below: PowerShell already matches
    # --tailscale to -Tailscale case-insensitively, and declaring an alias
    # that differs only in case is an outright error rather than a no-op.
    [switch]$Tailscale,
    [Alias('no-tailscale')][switch]$NoTailscale,
    # The key itself, for anybody scripting this. Left out, -Tailscale asks.
    [Alias('auth-key')][string]$AuthKey,
    # The hyphenated aliases are load-bearing, not decoration. PowerShell treats
    # a leading -- as a single dash, so --https binds to -Https on its own - but
    # --no-https becomes -no-https, and a parameter *name* cannot contain a
    # hyphen. Without the alias it bound to nothing and was ignored in silence:
    # the installer reported success and left the install on http.
    [Alias('no-https')][switch]$NoHttps,
    [Alias('no-shortcuts')][switch]$NoShortcuts,
    [Alias('no-auto-start')][switch]$NoAutoStart,
    [Alias('no-browser')][switch]$NoBrowser,
    # No alias: --library already binds to -Library, and an alias differing
    # only in case is an error rather than a no-op.
    [string]$Library
)

if ($Https -and $NoHttps) {
    Write-Host "  -Https and -NoHttps cannot both be given." -ForegroundColor Red
    exit 1
}
if ($Tailscale -and $NoTailscale) {
    Write-Host "  -Tailscale and -NoTailscale cannot both be given." -ForegroundColor Red
    exit 1
}

# Older .NET defaults this to SSL 3.0 and TLS 1.0, and GitHub has required TLS
# 1.2 since 2018 - so on an otherwise healthy machine every download below
# fails, with an error that blames the connection rather than the protocol.
#
# Only when it has been pinned to something. Left at SystemDefault, Windows
# picks the best protocol it has, which is better than anything named here -
# forcing Tls12 in that case would switch TLS 1.3 off on Windows 11.
try {
    if ([Net.ServicePointManager]::SecurityProtocol -ne [Net.SecurityProtocolType]::SystemDefault) {
        [Net.ServicePointManager]::SecurityProtocol =
            [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }
} catch {
    # A .NET too old to know SystemDefault, or too new to expose the enum.
}

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
    param([string[]]$Arguments, [switch]$Capture, [switch]$Calm)

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
        if ($Calm) {
            $lastBeat = Get-Date
            & docker @Arguments 2>&1 | ForEach-Object {
                $line = "$_"
                if (Test-DockerChurn $line) {
                    # Swallowed, but not silently: a download this long with
                    # nothing on screen is how somebody decides it has hung
                    # and closes the window.
                    if (((Get-Date) - $lastBeat).TotalSeconds -ge 30) {
                        Note "still downloading..."
                        $lastBeat = Get-Date
                    }
                    return
                }
                Write-Host $line
                $lastBeat = Get-Date
            }
        } else {
            # Piped through Write-Host rather than run bare: without this the
            # stderr lines still arrive as ErrorRecords and print as a red
            # NativeCommandError block, which looks like a crash to anybody
            # who has not seen one before. docker reports progress there.
            & docker @Arguments 2>&1 | ForEach-Object { Write-Host "$_" }
        }
        return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = '' }
    } finally {
        $ErrorActionPreference = $previousPreference
    }
}

# Test-DockerChurn picks out the lines docker prints over and over.
#
# Given a terminal, docker redraws one progress block in place. Given a pipe
# it cannot, and falls back to printing a whole line per progress tick - so a
# 3GB pull becomes many hundreds of lines of hex and megabytes scrolling past.
# The first person to install this watched that for ten minutes, which reads
# far more like a fault than like progress.
#
# The pipe is not the thing to remove: it is what stops docker's stderr
# arriving as ErrorRecords and printing as a red block that looks like a
# crash. So the churn is dropped here instead, and the milestones - what is
# being pulled, what finished, anything that went wrong - are kept.
function Test-DockerChurn([string]$Line) {
    # The colon is optional and that is the whole point: `docker pull` writes
    # "5c3b447848a9: Extracting", `docker compose pull` writes
    # "f5be9333d3a8 Extracting" with no colon at all - and compose is what
    # this script runs. A first version of this regexp required the colon and
    # would have filtered nothing whatsoever on the one command it is for.
    return $Line -match '^\s*[0-9a-f]{8,}:?\s+(Extracting|Downloading|Download complete|Waiting|Pulling fs layer|Verifying Checksum|Already exists|Pull complete)\b'
}

# Invoke-Native runs an external program without its stderr becoming fatal.
#
# PowerShell 5.1 wraps every stderr line from a native program in an
# ErrorRecord, and with $ErrorActionPreference = 'Stop' the first one throws.
# That is not a stylistic problem: `docker info` writes to stderr when the
# engine is not running, so the check for "is Docker running" crashed instead
# of answering false - in exactly the situation it exists to detect, which is
# the situation immediately after installing Docker Desktop.
#
# Every external call in this script goes through here or through Invoke-Docker.
function Invoke-Native {
    param([string]$Command, [string[]]$Arguments, [switch]$Show)

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        if ($Show) {
            # Printed as it arrives rather than collected: a multi-minute
            # download with a silent window is how somebody decides it hung.
            & $Command @Arguments 2>&1 | ForEach-Object { Write-Host "$_" }
            return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = '' }
        }
        $lines = & $Command @Arguments 2>&1 | ForEach-Object { "$_" }
        return [pscustomobject]@{
            ExitCode = $LASTEXITCODE
            Output   = ($lines -join [Environment]::NewLine)
        }
    } catch {
        return [pscustomobject]@{ ExitCode = 1; Output = $_.Exception.Message }
    } finally {
        $ErrorActionPreference = $previousPreference
    }
}

function Test-DockerRunning {
    return ((Invoke-Native 'docker' @('info')).ExitCode -eq 0)
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

# Hide-DockerDashboard stops Docker Desktop opening its window on every start.
#
# Only called immediately after installing it, so this sets a default on a
# fresh install rather than overriding a choice somebody made. Nobody who
# installs SoundStorm wants a Docker dashboard in their face at every login -
# the whole premise is that they never learn Docker is there.
#
# Written without a byte order mark: PowerShell 5.1's Set-Content -Encoding
# utf8 adds one, and a BOM in front of a JSON document is a good way to find
# out whether the reader is strict.
function Hide-DockerDashboard {
    param([switch]$Quiet)

    try {
        $dir = Join-Path $env:APPDATA 'Docker'
        $file = Join-Path $dir 'settings-store.json'
        if (-not (Test-Path $file)) {
            $legacy = Join-Path $dir 'settings.json'
            if (Test-Path $legacy) { $file = $legacy }
        }

        if (Test-Path $file) {
            $settings = Get-Content $file -Raw | ConvertFrom-Json
        } else {
            New-Item -ItemType Directory -Force -Path $dir | Out-Null
            $settings = New-Object psobject
        }

        # -Force so this works whether or not the key is already there. Docker
        # only writes settings that differ from its defaults, so on a fresh
        # install it will be absent.
        $settings | Add-Member -NotePropertyName 'OpenUIOnStartupDisabled' `
            -NotePropertyValue $true -Force

        $json = $settings | ConvertTo-Json -Depth 20
        [IO.File]::WriteAllText($file, $json, (New-Object Text.UTF8Encoding $false))
        if (-not $Quiet) {
            Note "Docker Desktop will stay out of the way in the system tray."
        }
    } catch {
        # Cosmetic. Never worth failing an install over.
    }
}

# Get-LanAddress is this machine's address on the local network.
#
# Needed because the container cannot work this out for itself - inside Docker
# the only addresses visible are the container's own - and because telling
# somebody their media server is at "localhost" is useless the moment they pick
# up a phone.
#
# 192.168 first, then 10., then the 172.16-31 range, because that last one is
# also where Docker and WSL put their virtual adapters and those reach nothing.
function Get-LanAddress {
    try {
        $addresses = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop |
            Where-Object {
                $_.IPAddress -notlike '127.*' -and
                $_.IPAddress -notlike '169.254.*' -and
                $_.PrefixOrigin -ne 'WellKnown'
            } | Sort-Object InterfaceMetric

        foreach ($pattern in @('192.168.*', '10.*', '172.*')) {
            $match = $addresses | Where-Object { $_.IPAddress -like $pattern } | Select-Object -First 1
            if ($match) { return $match.IPAddress }
        }
        if ($addresses) { return ($addresses | Select-Object -First 1).IPAddress }
    } catch {
        # Not worth a failed install.
    }
    return $null
}

# There is deliberately no ".local" name printed on Windows.
#
# An earlier version printed "<computer>.local" as the address to use, having
# checked that it resolved. That check was worthless: it ran on the machine
# itself, where Windows answers for its own hostname regardless, so it proved
# nothing about whether a phone could resolve it. It passed on the development
# machine and failed on the first other PC it was tried on.
#
# The reason is that Windows does not reliably advertise its hostname over
# mDNS. What was answering on port 5353 here turned out to be calibre-server
# and steamwebhelper - unrelated applications that happen to run a responder -
# with no Bonjour service installed at all. macOS and Linux with avahi do
# advertise properly, which is why install.sh still offers it there.
#
# An address that works everywhere beats a nicer one that works on the machine
# that printed it.

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    return (New-Object Security.Principal.WindowsPrincipal $identity).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

# Refresh-Path picks up what an installer just added.
#
# winget does not update the PATH of the session that called it, so `docker`
# stays unresolvable until a new window is opened - which looks exactly like
# the install having failed.
function Refresh-Path {
    $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
                [Environment]::GetEnvironmentVariable('Path', 'User')
}

# Install-Docker uses winget, which ships with Windows 10 1809 and later.
#
# The alternative is telling somebody to visit a website, pick the right
# download and run an installer, which is the single step this script exists
# to remove.
#
# Docker Desktop's installer needs administrator rights, and a setup file run
# by double-clicking does not have them - so this step asks for them, once,
# with a UAC prompt. Without that winget fails and the whole install stops on
# its very first action.
# Invoke-Elevated runs one command as administrator.
#
# Returns its exit code, or $null when the prompt was refused or never
# appeared - which is a different failure from the command running and
# failing, and gets a different message.
function Invoke-Elevated([string]$File, [string[]]$Arguments) {
    if (Test-Administrator) {
        return (Invoke-Native $File $Arguments -Show).ExitCode
    }
    try {
        $process = Start-Process -FilePath $File -ArgumentList $Arguments `
            -Verb RunAs -PassThru -Wait -ErrorAction Stop
        # Reading .Handle caches it; without one ExitCode is unreliable on a
        # process started this way. Same trap as Invoke-DockerBounded.
        $null = $process.Handle
        return $process.ExitCode
    } catch {
        return $null
    }
}

# Test-WSL reports whether Windows Subsystem for Linux is there and modern
# enough for Docker's engine to run on.
#
# wsl.exe ships in System32 on every Windows 10 and 11 whether or not WSL is
# actually installed, so finding the command proves nothing. `--version` is
# the question that answers only where the real thing is present, and its exit
# code is the whole answer - the text it prints is UTF-16 and arrives full of
# null bytes through a pipe.
function Test-WSL {
    if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) { return $false }
    return ((Invoke-Native 'wsl.exe' @('--version')).ExitCode -eq 0)
}

# Install-WSL is the second thing a new PC needs, and the second thing nobody
# is told about until Docker refuses to start.
#
# Docker Desktop runs its engine inside WSL2. On a machine that has never had
# it, Docker installs happily, launches, and then puts up a dialog asking for
# WSL to be installed or updated - a command the user now has to find, run as
# administrator, and follow with a restart. That is three steps past where an
# installer should have stopped asking, and it is where the first person to
# use this got stuck after the BIOS.
function Install-WSL {
    if (Test-WSL) { return }

    Step "Setting up Windows Subsystem for Linux"
    Note "Docker runs on this, and it is missing or out of date."
    if (-not (Test-Administrator)) {
        Note "Windows will ask for permission - say yes."
    }

    # --no-distribution because Docker brings its own. Without it Windows also
    # fetches Ubuntu: a gigabyte, several more minutes, and a first-run prompt
    # asking for a Linux username that nobody here will ever use again.
    $code = Invoke-Elevated 'wsl.exe' @('--install', '--no-distribution')

    if ($null -eq $code) {
        Stop-With @"
  Installing Windows Subsystem for Linux needs permission, and that was
  refused or dismissed. Docker cannot run without it.

  Run this setup again and choose Yes when Windows asks.
"@
    }

    if ($code -ne 0) {
        # A Windows too old to know --no-distribution, or a WSL that is
        # present but stale and wants updating rather than installing.
        $null = Invoke-Elevated 'wsl.exe' @('--update')
    }

    Refresh-Path
    if (Test-WSL) {
        Good "Windows Subsystem for Linux is ready."
        return
    }

    Stop-With @"
  Windows Subsystem for Linux has to be there before Docker can run, and it
  is not finished yet.

  This nearly always just needs a restart:

    1. Restart the PC.
    2. Run this setup again - it picks up where it left off, and nothing
       you have already downloaded is lost.

  If it stops here a second time, open PowerShell as Administrator, run

    wsl --install --no-distribution

  then restart and run this setup again.
"@
}

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

    # Written before the install as well as after it. Docker Desktop launches
    # itself the moment its installer finishes, which is too early for anything
    # this script does afterwards to prevent - but it reads this file on that
    # first launch, so putting the setting there first is the only way to stop
    # the window ever appearing.
    Hide-DockerDashboard -Quiet

    $wingetArgs = @(
        'install', '--exact', '--id', 'Docker.DockerDesktop',
        '--accept-source-agreements', '--accept-package-agreements', '--silent'
    )

    if (Test-Administrator) {
        $code = (Invoke-Native 'winget' $wingetArgs -Show).ExitCode
    } else {
        Note "Windows will ask for permission to install it - say yes."
        try {
            $process = Start-Process -FilePath 'winget' -ArgumentList $wingetArgs `
                -Verb RunAs -PassThru -Wait -ErrorAction Stop
            # Reading .Handle caches it; without one ExitCode is unreliable on
            # a process started this way.
            $null = $process.Handle
            $code = $process.ExitCode
        } catch {
            Stop-With @"
  Installing Docker Desktop needs permission, and that was refused or
  cancelled.

  Run the setup again and choose Yes when Windows asks - or install Docker
  Desktop yourself from here and then run the setup again:

    https://www.docker.com/products/docker-desktop/
"@
        }
    }

    Refresh-Path

    # Whether it worked is better answered by looking than by decoding an exit
    # code. winget has a family of them - 0 is installed, 0x8A150061 is already
    # installed, and a reboot-required result is a success that reads like a
    # failure - so the question asked here is simply whether docker is there
    # now.
    if (Get-Command docker -ErrorAction SilentlyContinue) {
        Good "Docker Desktop installed."
        Hide-DockerDashboard
        return
    }

    Stop-With @"
  Docker Desktop did not finish installing. (winget exit code: $code)

  This is usually one of two things:

    * it needs a restart to finish - restart the PC, then run this again
    * Windows features for virtualisation are off - Docker Desktop will say
      so if you open it from the Start menu

  Or install it yourself from here and run the setup again:

    https://www.docker.com/products/docker-desktop/
"@
}

# Test-Virtualization answers whether this PC can run Docker at all.
#
# Docker on Windows runs Linux in a lightweight virtual machine, so hardware
# virtualization is not optional. Essentially every CPU since 2008 has it and
# a great many prebuilt desktops ship with it switched off in the firmware,
# which is a thing only a trip into the BIOS can change.
#
# Asked before the download rather than after, because the alternative is what
# happened to the first person who ran this: 500MB of Docker Desktop
# installed, and only then "virtualization support wasn't detected" - leaving
# a program they cannot use, on a machine they now have to go and fix anyway,
# with nothing on screen explaining which of those two things went wrong.
#
# True when it cannot tell. Refusing to install on a machine that is actually
# fine is a worse failure than the check never firing, and this is a guess
# about firmware read through two layers of Windows.
function Test-Virtualization {
    try {
        $system = Get-CimInstance -ClassName Win32_ComputerSystem -ErrorAction Stop
    } catch {
        return $true
    }
    if (-not $system) { return $true }

    # A running hypervisor settles it: Hyper-V or WSL2 is already up, and
    # neither can be without virtualization. This has to be asked first,
    # because once a hypervisor is present Windows reports
    # VirtualizationFirmwareEnabled as false regardless - it can no longer see
    # the firmware to ask. Checking the other property first would read a
    # perfectly working PC as a broken one.
    if ($system.HypervisorPresent) { return $true }

    # Explicitly false, not merely missing. An older Windows may not populate
    # this at all, and absent means unknown rather than off.
    $property = $system.PSObject.Properties['VirtualizationFirmwareEnabled']
    if ($property -and $system.VirtualizationFirmwareEnabled -eq $false) {
        return $false
    }
    return $true
}

# The one failure this script cannot work around, so it gets the whole recipe
# rather than a line saying to go and look it up.
function Stop-ForVirtualization {
    Stop-With @"
  This PC has hardware virtualization turned off, and Docker cannot run
  without it. Nothing has been installed.

  It is switched off rather than missing, on almost every PC this happens
  to, and turning it on means a trip into the BIOS:

    1. Restart the PC and press the setup key as it starts - usually Del or
       F2. (Dell: F2.  HP: F10.  Lenovo: F1.)
    2. Find "Intel Virtualization Technology", "Intel VT-x", or on an AMD
       machine "SVM Mode". It is usually under Advanced, CPU Configuration
       or Security.
    3. Set it to Enabled, then Save and Exit.
    4. Run this setup again.

  To check it worked: Ctrl+Shift+Esc, the Performance tab, click CPU, and
  read the Virtualization line on the right.
"@
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
    Note "If it opens a window asking you to accept its terms, say yes -"
    Note "SoundStorm will carry on by itself once you have."
    Start-Process -FilePath $exe | Out-Null

    $waited = 0
    while (-not (Test-DockerRunning)) {
        Start-Sleep -Seconds 3
        $waited += 3
        if ($waited % 30 -eq 0) { Note "still starting... ($waited seconds)" }
        if ($waited -gt 420) {
            # Docker was already installed when this run started, so the
            # check above never ran. It is worth asking now: an engine that
            # never comes up is exactly what a firmware setting being off
            # looks like from here.
            if (-not (Test-Virtualization)) { Stop-ForVirtualization }
            Stop-With @"
  Docker Desktop was started but its engine never came up.

  On a brand new install it usually wants one of these first:

    * its terms accepted - open Docker Desktop from the Start menu and
      see whether it is waiting on a window
    * Windows Subsystem for Linux - if Docker is asking you to install or
      update WSL, run this setup again and it will do it for you
    * a restart of the PC

  Do whichever it asks for, then run this setup again. Nothing is lost -
  it picks up where it left off.
"@
        }
    }
    Good "Docker is running."
}

function Initialize-Docker {
    $installed = [bool](Get-Command docker -ErrorAction SilentlyContinue)

    # Only where somebody is sitting in front of it. The desktop shortcut runs
    # this minimised at startup, and a permission prompt with no visible
    # window behind it is worse than the failure it would be fixing.
    if (-not $Launch) {
        # Before the download, not after. Docker Desktop is half a gigabyte
        # and installing it on a machine that cannot run it helps nobody.
        if (-not $installed -and -not (Test-Virtualization)) { Stop-ForVirtualization }
        # And before Docker rather than after, because Docker's installer
        # assumes WSL is already there. Checked even when Docker is present:
        # "installed but will not start" is most often a stale WSL, which is
        # exactly what Docker's own dialog asks you to go and fix by hand.
        Install-WSL
    }

    if (-not $installed) {
        Install-Docker
    }
    Refresh-Path
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Stop-With @"
  Docker Desktop is installed but Windows has not picked it up in this
  window yet.

  Restart the PC and run the setup again - a fresh Docker install usually
  wants one anyway.
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

# Test-Healthz asks once and answers true or false.
#
# HttpWebRequest rather than Invoke-WebRequest, and that is not a preference.
# PowerShell 5.1 has no -SkipCertificateCheck, so trusting our own self-signed
# certificate means assigning ServicePointManager.ServerCertificateValidationCallback
# - and with a scriptblock in that callback, Invoke-WebRequest fails against
# *every* https address, ours and github.com alike, with "An unexpected error
# occurred on a send". It runs the request off the pipeline thread, where there
# is no runspace to execute a scriptblock in, so the validation delegate throws
# and the connection is torn down. The error names the send, never the callback.
# HttpWebRequest.GetResponse() runs on the pipeline thread and is fine.
function Test-Healthz([string]$Url) {
    try {
        $request = [Net.HttpWebRequest]::Create("$Url/healthz")
        $request.Timeout = 5000
        $request.Method = 'GET'
        $response = $request.GetResponse()
        $response.Close()
        return $true
    } catch {
        return $false
    }
}

function Wait-ForSoundStorm([string]$Url) {
    # Process-wide, because .NET Framework offers no per-request hook. Set for
    # the few seconds of the health check and put back afterwards; the requests
    # it covers go to a certificate this machine minted, on this machine.
    $priorCallback = $null
    $bypassed = $false
    if ($Url -like 'https://*') {
        $priorCallback = [Net.ServicePointManager]::ServerCertificateValidationCallback
        [Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
        $bypassed = $true
    }
    try {
        $waited = 0
        while (-not (Test-Healthz $Url)) {
            Start-Sleep -Seconds 2
            $waited += 2
            if ($waited -gt 180) {
                Stop-With "  SoundStorm started but never answered on $Url.`n`n  Show this to whoever gave you the app:`n`n    cd `"$Dir`"; docker compose logs soundstorm"
            }
        }
    } finally {
        if ($bypassed) { [Net.ServicePointManager]::ServerCertificateValidationCallback = $priorCallback }
    }
}

# Takes the folder, because the one thing this has to read is sometimes
# somebody else's install: when the setup refuses because SoundStorm is
# already installed elsewhere, the useful half of that message is the address
# of the install it found.
function Get-EnvSettingIn([string]$Folder, [string]$Name) {
    $envFile = Join-Path $Folder '.env'
    if (-not (Test-Path $envFile)) { return $null }
    foreach ($line in (Get-Content $envFile)) {
        if ($line -match "^\s*$([regex]::Escape($Name))=(.*)$") { return $Matches[1].Trim() }
    }
    return $null
}

function Get-EnvSetting([string]$Name) {
    return Get-EnvSettingIn $Dir $Name
}

# Get-LibraryPath is where this install keeps its media: the folder beside it,
# unless .env says otherwise. Read from .env every time, so the shortcuts, the
# folders and the uninstaller can never disagree about it.
function Get-LibraryPath {
    $chosen = Get-EnvSetting 'SOUNDSTORM_LIBRARY_PATH'
    if ($chosen) { return ($chosen -replace '/', '\') }
    return (Join-Path $Dir 'library')
}

# Get-InstalledURL is where an install answers, read from its own .env rather
# than assumed. Falls back to the first port and plain http, which is what a
# .env too old to carry either of them meant.
function Get-InstalledURL([string]$Folder) {
    $port = Get-EnvSettingIn $Folder 'SOUNDSTORM_PORT'
    if ($port -notmatch '^\d+$') { $port = "$FirstPort" }
    $scheme = ConvertTo-Scheme (Get-EnvSettingIn $Folder 'SOUNDSTORM_TLS')
    return "${scheme}://localhost:$port"
}

# ConvertTo-Scheme is the scheme to hand somebody for a TLS setting. Auto mode
# is http: it answers http and https on the same port, http works from the
# first second, and the page moves itself to the real https address once it
# has checked this browser can reach it. Only self-signed and file are https
# alone.
function ConvertTo-Scheme([string]$Tls) {
    if ($Tls -eq 'self-signed' -or $Tls -eq 'file') { return 'https' }
    return 'http'
}

# Set-EnvSetting rewrites one line of .env and leaves the rest alone, because
# the port and the certificate hosts are in there too and were worked out on a
# run nobody is going to repeat.
function Set-EnvSetting([string]$Name, [string]$Value) {
    $envFile = Join-Path $Dir '.env'
    $lines = @()
    if (Test-Path $envFile) { $lines = @(Get-Content $envFile) }
    $pattern = "^\s*$([regex]::Escape($Name))="
    $kept = @($lines | Where-Object { $_ -notmatch $pattern })
    $kept += "$Name=$Value"
    $kept | Out-File -FilePath $envFile -Encoding ascii
}

function Get-InstalledPort {
    $port = Get-EnvSetting 'SOUNDSTORM_PORT'
    if ($port -match '^\d+$') { return [int]$port }
    return $FirstPort
}

# Write-ServeConfig writes the file Tailscale proxies through.
#
# The scheme matters and is the one thing that cannot be a constant: Tailscale
# talks to SoundStorm over the internal compose network, and SoundStorm is
# either speaking plain HTTP there or its own self-signed HTTPS depending on
# what -Https did. Point the proxy at the wrong one and the tailnet address
# answers 502 while everything else looks fine.
#
# https+insecure is Tailscale's documented pseudo-scheme for a backend with a
# certificate nothing can validate, which is exactly what a local authority
# issues. The hop is inside Docker's own network either way.
function Write-ServeConfig {
    $target = if ((Get-InstalledScheme) -eq 'https') {
        'https+insecure://soundstorm-app:8080'
    } else {
        'http://soundstorm-app:8080'
    }
    $json = @"
{
  "TCP": { "443": { "HTTPS": true } },
  "Web": {
    "`${TS_CERT_DOMAIN}:443": {
      "Handlers": {
        "/": { "Proxy": "$target" }
      }
    }
  }
}
"@
    $json | Out-File -FilePath (Join-Path $Dir 'tailscale-serve.json') -Encoding ascii
}

# Get-TailnetURL asks the running Tailscale container where it ended up.
#
# The address is assigned by Tailscale, not chosen here - it is the hostname
# plus whatever the tailnet is called - so the only honest way to print it is
# to ask after the fact.
function Get-TailnetURL {
    for ($waited = 0; $waited -lt 60; $waited += 3) {
        $status = Invoke-Docker @('exec', 'soundstorm-tailscale', 'tailscale', 'status', '--json') -Capture
        if ($status.ExitCode -eq 0) {
            try {
                $parsed = $status.Output | ConvertFrom-Json
                $name = $parsed.Self.DNSName
                if ($name) { return "https://" + $name.TrimEnd('.') }
            } catch {
                # Still coming up; it prints something that is not JSON yet.
            }
        }
        Start-Sleep -Seconds 3
    }
    return ''
}

# Get-InstalledScheme reads what this install is actually serving rather than
# assuming http. Telling somebody the wrong scheme hands them a browser error
# with no hint in it, which is worse than telling them nothing.
function Get-InstalledScheme {
    return ConvertTo-Scheme (Get-EnvSetting 'SOUNDSTORM_TLS')
}

# Get-SecureAddress waits briefly for auto mode's real https address, asking
# SoundStorm itself over plain http on this machine - so no certificate is
# involved in the asking. The name arrives within seconds of the certificate,
# which usually takes ten or twenty; empty if it has not by the deadline, and
# the http address works meanwhile.
function Get-SecureAddress([int]$Port) {
    for ($waited = 0; $waited -lt 45; $waited += 3) {
        try {
            $request = [Net.HttpWebRequest]::Create("http://localhost:$Port/api/session")
            $request.Timeout = 5000
            $response = $request.GetResponse()
            $reader = New-Object IO.StreamReader($response.GetResponseStream())
            $body = $reader.ReadToEnd()
            $response.Close()
            $name = ($body | ConvertFrom-Json).secureName
            if ($name) { return "https://${name}:$Port" }
        } catch {
            # Still starting; ask again.
        }
        Start-Sleep -Seconds 3
    }
    return ''
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
        (Get-LibraryPath) $null $null 'Put your music, films and books in here' $false

    # Updating is re-running the installer, so the shortcut is the installer.
    New-Shortcut (Join-Path $startMenu 'Update SoundStorm.lnk') $powershell `
        "-NoProfile -ExecutionPolicy Bypass -File `"$localScript`"" $Dir `
        'Get the newest version of SoundStorm' $false

    if (-not $NoAutoStart) {
        $startup = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Startup'
        New-Shortcut (Join-Path $startup 'SoundStorm.lnk') $powershell `
            "-NoProfile -ExecutionPolicy Bypass -File `"$localScript`" -Launch -NoBrowser" $Dir `
            'Start SoundStorm with Windows' $true
    }
    Register-Uninstaller
    Good "Added SoundStorm to the Start menu and the desktop."
}

# uninstallKey is where Windows looks for what can be removed.
#
# Under HKCU rather than HKLM because SoundStorm installs per-user, into the
# user's own folder, without administrator rights. It shows up in Settings,
# Apps, where people actually go to remove something - a program that can only
# be uninstalled by finding instructions on a web page is not really
# uninstallable.
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\SoundStorm'

function Register-Uninstaller {
    try {
        $localScript = Join-Path $Dir 'soundstorm.ps1'
        $powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'

        New-Item -Path $uninstallKey -Force | Out-Null

        # Split by type rather than choosing one inline: PowerShell 5.1 will
        # not take an `if` as an argument expression, and a tokenizer check
        # does not catch that.
        $strings = @{
            DisplayName     = 'SoundStorm'
            DisplayVersion  = '0.1'
            Publisher       = 'SoundStorm'
            InstallLocation = $Dir
            URLInfoAbout    = 'https://github.com/GabrielHollberg/soundstorm'
            UninstallString = "`"$powershell`" -NoProfile -ExecutionPolicy Bypass -File `"$localScript`" -Uninstall"
        }
        foreach ($name in $strings.Keys) {
            New-ItemProperty -Path $uninstallKey -Name $name -Value $strings[$name] `
                -PropertyType String -Force | Out-Null
        }
        foreach ($name in @('NoModify', 'NoRepair')) {
            New-ItemProperty -Path $uninstallKey -Name $name -Value 1 `
                -PropertyType DWord -Force | Out-Null
        }
    } catch {
        # Being absent from the app list is untidy, not broken.
        Note "Could not register the uninstaller: $($_.Exception.Message)"
    }
}

function Remove-Shortcuts {
    $desktop = [Environment]::GetFolderPath('Desktop')
    $programs = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
    foreach ($path in @(
        (Join-Path $desktop 'SoundStorm.lnk'),
        (Join-Path $desktop 'SoundStorm media.lnk'),
        (Join-Path $programs 'SoundStorm.lnk'),
        (Join-Path $programs 'Update SoundStorm.lnk'),
        (Join-Path $programs 'Startup\SoundStorm.lnk')
    )) {
        Remove-Item $path -Force -ErrorAction SilentlyContinue
    }
}

# --- removing it ---------------------------------------------------------------

if ($Uninstall) {
    Write-Host ""
    Write-Host "  Removing SoundStorm" -ForegroundColor White
    Write-Host "  -----------------------------------------------------------"

    $library = Get-LibraryPath
    $hasLibrary = Test-Path $library

    if (-not (Test-Path (Join-Path $Dir 'docker-compose.yml'))) {
        Note "Nothing installed in $Dir - tidying up shortcuts anyway."
    } else {
        Set-Location $Dir
        if (Get-Command docker -ErrorAction SilentlyContinue) {
            # A copy first, into the folder rather than the volume about to be
            # deleted. This is the exact moment the credentials for four
            # backends stop existing anywhere, and somebody uninstalling to
            # move machines has no other warning that they were about to.
            Step "Saving your accounts first"
            $backup = Join-Path $Dir 'soundstorm-backup.json'
            $saved = Invoke-Docker @(
                'compose', 'run', '--rm', '-v', "${Dir}:/backup",
                'soundstorm', 'backup', '/backup/soundstorm-backup.json'
            ) -Capture
            if ($saved.ExitCode -eq 0 -and (Test-Path $backup)) {
                Good "Saved to $backup"
                Note "Keep it if you might reinstall - it is the only copy of the"
                Note "passwords SoundStorm made on the media servers."
            } else {
                # Not fatal: somebody uninstalling has asked to lose this, and
                # refusing to uninstall because the backup failed is worse.
                Note "Could not save a copy. Carrying on with the uninstall."
            }

            Step "Stopping it and removing its data"
            Note "Accounts and the servers' own settings go; your media does not."
            # down -v takes the named volumes with it: SoundStorm's accounts,
            # and Jellyfin's and Navidrome's own databases. The library is a
            # bind mount from the folder and is not touched by this.
            Invoke-Docker @('compose', 'down', '-v') -Capture | Out-Null
        } else {
            Note "Docker is not available, so the containers were left alone."
        }
    }

    Step "Removing shortcuts"
    Remove-Shortcuts
    Remove-Item $uninstallKey -Recurse -Force -ErrorAction SilentlyContinue
    Good "Shortcuts removed."

    Step "Cleaning up the folder"
    # soundstorm-backup.json is deliberately not in this list. It is the only
    # thing here worth keeping, and the moment somebody wants it is after they
    # have already uninstalled.
    foreach ($leftover in @('docker-compose.yml', '.env', 'soundstorm.ps1', 'tailscale-serve.json')) {
        Remove-Item (Join-Path $Dir $leftover) -Force -ErrorAction SilentlyContinue
    }

    Write-Host ""
    Write-Host "  -----------------------------------------------------------"
    Write-Host "  Done." -ForegroundColor Green -NoNewline
    Write-Host " SoundStorm is gone."
    Write-Host ""
    if ($hasLibrary) {
        Write-Host "  Your media has been left exactly where it was:"
        Write-Host ""
        Write-Host "    $library"
        Write-Host ""
        Write-Host "  Delete that folder yourself if you want it gone. Nothing else"
        Write-Host "  will touch it."
    } else {
        Write-Host "  There was no media library to keep."
    }
    Write-Host ""
    Write-Host "  Docker Desktop was left installed - other things may be using it."
    Write-Host ""
    exit 0
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
    $url = "$(Get-InstalledScheme)://localhost:$port"
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
Note (Invoke-Native 'docker' @('--version')).Output

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
    # The address as well as the folder. "Use the one that is already there"
    # is not an instruction if it does not say how, and somebody who ran this
    # a second time is quite likely to have run it because they could not
    # remember where it was.
    $existing = Get-InstalledURL $previous
    Stop-With @"
  SoundStorm is already installed, in another folder:

    $previous

  It should be running. Open it here:

    $existing

  Installing it here as well would not give you a second copy - both folders
  drive the same containers, and this one would point at an empty library, so
  your media would look like it had vanished.

  To move it here instead, remove the old one first: open a terminal in the
  folder above and run

    docker compose down

  then run this setup again.
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

# Always, whether or not Tailscale is wanted. compose bind-mounts this file,
# and Docker's answer to a bind mount whose source is missing is to create a
# *directory* with that name - after which the container fails in a way that
# reads like a Tailscale problem rather than a missing file.
if (-not (Test-Path (Join-Path $Dir 'tailscale-serve.json'))) { Write-ServeConfig }

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

    # Compose reads .env from beside the compose file, so these stick.
    #
    # The LAN address is written even though TLS is off, because it is needed
    # the moment somebody turns TLS on and it cannot be worked out then: the
    # server is in a container and sees only the container's addresses. Better
    # recorded now, while the machine that knows is the one running.
    $lines = @("SOUNDSTORM_PORT=$port")
    $lan = Get-LanAddress
    if ($lan) { $lines += "SOUNDSTORM_TLS_HOSTS=$lan" }
    $lines | Out-File -FilePath '.env' -Encoding ascii
}

# After the port, so that on a fresh install this amends the file just written
# rather than being overwritten by it.
#
# Auto is the default: a real certificate for a <id>.home.soundstorm.dev name,
# with plain http still answering on the same port. It is written for a fresh
# install and for an existing one that never chose - an absent line meant
# "off" only because off was the default then. A choice somebody made (off,
# self-signed, file) is left alone.
$tlsNow = Get-EnvSetting 'SOUNDSTORM_TLS'
if ($Https -or ($NoHttps -eq $false -and -not $tlsNow)) {
    # Auto points its name at the LAN address, and only this machine can say
    # what that is - the server sees the container's address, not the PC's.
    # An install from before .env carried this line has to be topped up here.
    if (-not (Get-EnvSetting 'SOUNDSTORM_TLS_HOSTS')) {
        $lan = Get-LanAddress
        if ($lan) { Set-EnvSetting 'SOUNDSTORM_TLS_HOSTS' $lan }
    }
    Set-EnvSetting 'SOUNDSTORM_TLS' 'auto'
    if ($Https -or $upgrade) { Note "Turning on https." }
} elseif ($NoHttps) {
    Set-EnvSetting 'SOUNDSTORM_TLS' 'off'
    Note "Turning https off."
}
$tlsMode = Get-EnvSetting 'SOUNDSTORM_TLS'

# The first sign-up needs a setup code, so that whoever reaches the port
# before the owner does - from the internet, once it faces it - cannot claim
# the server. It goes into the address the browser is opened at below, so
# nobody installing ever sees it. Kept once written: a second run must open
# the page with the code the server already has.
$setupCode = Get-EnvSetting 'SOUNDSTORM_SETUP_CODE'
if (-not $setupCode) {
    $bytes = New-Object byte[] 10
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $setupCode = -join ($bytes | ForEach-Object { $_.ToString('x2') })
    Set-EnvSetting 'SOUNDSTORM_SETUP_CODE' $setupCode
}

# Where the library lives. Beside the install unless -Library says otherwise,
# which is how it goes on an external drive. Compose mounts every shelf from
# the same setting, so they all follow.
#
# Existing media is never moved for anybody: tens of gigabytes shifted by a
# script is exactly the operation that should not fail halfway. Somebody moving
# the library is told where the old files are, and how.
if ($Library) {
    try {
        $full = [IO.Path]::GetFullPath($Library)
        New-Item -ItemType Directory -Force -Path $full -ErrorAction Stop | Out-Null
    } catch {
        Stop-With "  Could not use $Library for the library: $($_.Exception.Message)`n`n  Check the drive is connected, then run the setup again."
    }
    $previous = Get-LibraryPath
    Set-EnvSetting 'SOUNDSTORM_LIBRARY_PATH' ($full -replace '\\', '/')
    # What the app shows as the library's location: the path a person would
    # type into Explorer, not the one Docker is given.
    Set-EnvSetting 'SOUNDSTORM_LIBRARY_HINT' $full
    Note "Keeping the library in $full"
    if ($previous -ne $full -and (Test-Path $previous) -and
        (Get-ChildItem $previous -Recurse -File -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -ne 'README.txt' } | Select-Object -First 1)) {
        Write-Host ""
        Write-Host "  Your existing media is still in $previous." -ForegroundColor Yellow
        Write-Host "  To bring it across, close SoundStorm, move the folders inside it" -ForegroundColor DarkGray
        Write-Host "  into $full, and open SoundStorm again." -ForegroundColor DarkGray
        Write-Host ""
    }
}
$libraryPath = Get-LibraryPath

foreach ($folder in 'music', 'movies', 'tv', 'audiobooks', 'ebooks', 'documents', 'pictures') {
    New-Item -ItemType Directory -Force -Path (Join-Path $libraryPath $folder) | Out-Null
}
$scheme = Get-InstalledScheme

# Remote access. Off unless asked for, and it stays a separate decision from
# -Https: one is about the wifi at home, the other about being away from it.
if ($Tailscale) {
    $key = $AuthKey
    if (-not $key) { $key = Get-EnvSetting 'SOUNDSTORM_TAILSCALE_AUTHKEY' }
    if (-not $key) {
        Write-Host ""
        Write-Host "  Reaching SoundStorm from outside the house needs a Tailscale account."
        Write-Host "  It is free for personal use and takes about two minutes."
        Write-Host ""
        Write-Host "    1. Sign up at https://tailscale.com"
        Write-Host "    2. Open the admin console, Settings, then Keys"
        Write-Host "    3. Generate an auth key and copy it"
        Write-Host ""
        # The one prompt in this whole script, and only on a flag somebody
        # typed on purpose. A double-click install never reaches it.
        $key = Read-Host "  Paste the auth key here"
        $key = $key.Trim()
    }
    if (-not $key) {
        Stop-With @"
  No auth key, so there is nothing to connect with.

  SoundStorm is installed and working on this network either way - run the
  setup again with -Tailscale when you have a key.
"@
    }
    Set-EnvSetting 'SOUNDSTORM_TAILSCALE_AUTHKEY' $key
    Write-ServeConfig
    Note "Tailscale will be started with SoundStorm."
} elseif ($NoTailscale) {
    Set-EnvSetting 'SOUNDSTORM_TAILSCALE_AUTHKEY' ''
    Note "Turning off remote access. SoundStorm stays on this network."
}

# Whether the profile is wanted at all, which outlives this run: somebody who
# set it up in January should still get it after an upgrade in June.
$useTailscale = $false
if (-not $NoTailscale) {
    $useTailscale = [bool](Get-EnvSetting 'SOUNDSTORM_TAILSCALE_AUTHKEY')
}
$composeArgs = @()
if ($useTailscale) { $composeArgs = @('--profile', 'tailscale') }

if ($upgrade) {
    Step "Checking for a newer version"
} else {
    Step "Downloading the media servers"
    Note "About 8GB the first time. This is the long part - leave it running."
}
# Shown rather than captured: this is the part that takes minutes, and a
# silent window is how somebody decides it has hung.
$pull = Invoke-Docker (@('compose') + $composeArgs + @('pull')) -Calm
if ($pull.ExitCode -ne 0) {
    Stop-With "  Could not download the media servers. That is almost always the`n  internet connection. Try again - anything already downloaded is kept."
}

Step "Starting SoundStorm"
$start = Invoke-Docker (@('compose') + $composeArgs + @('up', '-d')) -Capture
if ($start.ExitCode -ne 0) {
    Write-Host $start.Output
    if ($start.Output -match 'already allocated|address already in use|forbidden by its access permissions') {
        Stop-With "  Port $port is already being used by another program on this PC.`n`n  Show this to whoever gave you the app."
    }
    Stop-With "  SoundStorm would not start.`n`n  Show this to whoever gave you the app:`n`n    cd `"$Dir`"; docker compose logs"
}

$url = "${scheme}://localhost:$port"
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
    Write-Host " SoundStorm is running at $url"
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

$lan = Get-LanAddress
$secure = ''
if ($tlsMode -eq 'auto') {
    Step "Getting a secure address"
    $secure = Get-SecureAddress $port
}
if ($secure) {
    # The real certificate is in: this address works with no warning on any
    # device, and a phone can install the app from it.
    Write-Host "  On your phone, TV or another computer on this network:"
    Write-Host ""
    Write-Host "    $secure" -ForegroundColor White
    Write-Host ""
    if ($lan) {
        Write-Host "  If that does not load, your router is refusing the name - use" -ForegroundColor DarkGray
        Write-Host "  http://${lan}:$port instead. Same account either way." -ForegroundColor DarkGray
    }
    Write-Host "  Worth saving as a bookmark." -ForegroundColor DarkGray
    Write-Host ""
} elseif ($lan) {
    Write-Host "  On your phone, TV or another computer on this network:"
    Write-Host ""
    Write-Host "    ${scheme}://${lan}:$port" -ForegroundColor White
    Write-Host ""
    if ($tlsMode -eq 'auto') {
        Write-Host "  SoundStorm is still getting its secure address, and moves there" -ForegroundColor DarkGray
        Write-Host "  by itself when it has one." -ForegroundColor DarkGray
    }
    Write-Host "  Same account. Worth saving as a bookmark - and worth giving this" -ForegroundColor DarkGray
    Write-Host "  PC a fixed address in your router, or that number will change." -ForegroundColor DarkGray
    Write-Host "  If nothing loads, allow SoundStorm through the Windows firewall" -ForegroundColor DarkGray
    Write-Host "  for private networks." -ForegroundColor DarkGray
    Write-Host ""
}
if ($useTailscale) {
    Step "Connecting to your tailnet"
    $tailnet = Get-TailnetURL
    Write-Host ""
    if ($tailnet) {
        Write-Host "  From anywhere, on any device signed into your tailnet:"
        Write-Host ""
        Write-Host "    $tailnet" -ForegroundColor White
        Write-Host ""
        Write-Host "  It works away from the house, with nothing forwarded on your" -ForegroundColor DarkGray
        Write-Host "  router." -ForegroundColor DarkGray
    } else {
        Write-Host "  Tailscale is starting but has not reported an address yet." -ForegroundColor Yellow
        Write-Host "  Check the Tailscale admin console, or run:" -ForegroundColor DarkGray
        Write-Host ""
        Write-Host "    docker logs soundstorm-tailscale" -ForegroundColor DarkGray
    }
    Write-Host ""
    Write-Host "  Every device that should reach it needs the Tailscale app and the" -ForegroundColor DarkGray
    Write-Host "  same account. There is no way around that part." -ForegroundColor DarkGray
    Write-Host ""
}

if ($tlsMode -eq 'self-signed') {
    # Said plainly and up front, because the alternative is somebody deciding
    # their own install is broken or unsafe. Nobody but this PC can vouch for a
    # certificate covering an address like 192.168.0.19, so the warning is
    # unavoidable without a real domain name - but it is fixable per device,
    # and that fix is the useful half of this message.
    Write-Host "  The first visit shows a certificate warning on every device." -ForegroundColor Yellow
    Write-Host "  That is expected: the certificate was made by this PC, and no" -ForegroundColor DarkGray
    Write-Host "  outside authority can vouch for a home network address." -ForegroundColor DarkGray
    Write-Host "  Choose Advanced, then continue." -ForegroundColor DarkGray
    Write-Host ""
    $caHost = if ($lan) { $lan } else { 'localhost' }
    Write-Host "  To stop it asking, open this on each device and install the"
    Write-Host "  certificate it downloads:"
    Write-Host ""
    Write-Host "    https://${caHost}:$port/ca.crt" -ForegroundColor White
    Write-Host ""
    Write-Host "  To go back to plain http, run the setup again with -NoHttps." -ForegroundColor DarkGray
    Write-Host ""
} elseif ($tlsMode -eq 'off') {
    Write-Host "  Run the setup again with -Https to encrypt the connection." -ForegroundColor DarkGray
    Write-Host ""
}
if (-not $NoShortcuts) {
    Write-Host "  Next time, click the SoundStorm icon on your desktop." -ForegroundColor DarkGray
    if (-not $NoAutoStart) {
        Write-Host "  It also starts by itself when you turn the PC on." -ForegroundColor DarkGray
    }
}
Write-Host ""

# With the setup code, which the page takes out of the address as it loads.
# Harmless once an account exists: it is only ever read by the first sign-up.
Start-Process "$url/?setup=$setupCode"
