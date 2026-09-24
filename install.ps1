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
#   -Remote        reach it from anywhere over the internet (off by default)
#   -NoRemote      keep it to the home network
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
    # Putting the server on the internet, off by default. Same hyphenated-alias
    # rule as -NoHttps above.
    [switch]$Remote,
    [Alias('no-remote')][switch]$NoRemote,
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
if ($Remote -and $NoRemote) {
    Write-Host "  -Remote and -NoRemote cannot both be given." -ForegroundColor Red
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

# raw.githubusercontent.com caches a branch URL for five minutes, and ignores a
# query string when it does - checked: a never-seen random query came back
# "X-Cache: HIT". So a fix pushed a minute ago reached a laptop as the version
# before it, and the setup showed the exact error the push had fixed. A commit
# URL cannot be stale, so the branch is resolved to its newest commit first,
# through the API (whose answer is fresh), and the branch URL is only the
# fallback when the API cannot be asked. Not on -Launch: opening the app must
# not wait on GitHub.
if (-not $Launch -and -not $env:SOUNDSTORM_COMPOSE_URL -and -not $env:SOUNDSTORM_SCRIPT_URL) {
    try {
        $commit = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/commits/$Branch" `
            -Headers @{ 'User-Agent' = 'soundstorm-installer'; 'Accept' = 'application/vnd.github+json' } `
            -TimeoutSec 15 -UseBasicParsing
        if ("$($commit.sha)" -match '^[0-9a-f]{40}$') {
            $RawBase = "https://raw.githubusercontent.com/$Repo/$($commit.sha)"
        }
    } catch {
        # Rate-limited or offline: the branch URL, at worst five minutes old.
    }
}
$ComposeUrl = if ($env:SOUNDSTORM_COMPOSE_URL) { $env:SOUNDSTORM_COMPOSE_URL } else { "$RawBase/docker-compose.yml" }
$ScriptUrl  = if ($env:SOUNDSTORM_SCRIPT_URL) { $env:SOUNDSTORM_SCRIPT_URL } else { "$RawBase/install.ps1" }

# Under the user's own folder rather than Program Files: the media library
# lives beside the compose file, and it has to be somewhere they can drop a
# hard drive of music into without a permission prompt.
$Dir       = if ($env:SOUNDSTORM_DIR) { $env:SOUNDSTORM_DIR } else { Join-Path $env:USERPROFILE 'SoundStorm' }
$FirstPort = if ($env:SOUNDSTORM_PORT) { [int]$env:SOUNDSTORM_PORT } else { 8099 }

# Updating runs the newest installer, not the one saved last time.
#
# "Update SoundStorm" runs the copy of this script saved beside the install,
# and the setup file runs whatever it just downloaded - so each update used to
# run the *previous* version's logic, and a fix to the installer itself only
# took effect on the update after the one that fetched it. So a saved or
# downloaded copy fetches the newest script and, when it differs, hands over to
# it with the same arguments. Only those two copies: a checkout being tested
# runs as it is. SOUNDSTORM_FRESH stops the new copy doing the same again.
$selfName = if ($PSCommandPath) { [IO.Path]::GetFileName($PSCommandPath) } else { '' }
if (-not $Launch -and $env:SOUNDSTORM_FRESH -ne '1' -and
    ($selfName -eq 'soundstorm.ps1' -or $selfName -eq 'soundstorm-install.ps1')) {
    $fresh = Join-Path $env:TEMP "soundstorm-fresh-$PID.ps1"
    $handOver = $false
    try {
        Invoke-WebRequest -Uri $ScriptUrl -OutFile $fresh -UseBasicParsing -TimeoutSec 30
        $newText = [IO.File]::ReadAllText($fresh)
        $oldText = [IO.File]::ReadAllText($PSCommandPath)
        $handOver = ($newText -ne $oldText -and $newText -match 'SOUNDSTORM_FRESH')
    } catch {
        # Offline, or GitHub unreachable: carry on with this copy, which is
        # what would have happened before.
    }
    if ($handOver) {
        # Outside the try above on purpose: a failure inside the new copy must
        # end here, not fall back to running this old one as well.
        $env:SOUNDSTORM_FRESH = '1'
        $code = 1
        try {
            & $fresh @PSBoundParameters
            $code = if ($null -ne $LASTEXITCODE) { $LASTEXITCODE } else { 0 }
        } catch {
            Write-Host "  $($_.Exception.Message)" -ForegroundColor Red
        } finally {
            Remove-Item -LiteralPath $fresh -Force -ErrorAction SilentlyContinue
        }
        exit $code
    }
    Remove-Item -LiteralPath $fresh -Force -ErrorAction SilentlyContinue
}

# Output. Notes are Gray, not the DarkGray they used to be: on Windows
# PowerShell's default dark-blue console DarkGray is close to unreadable, and
# nearly everything this script says is something the person needs to read.
# Steps are Cyan so the numbered progress stands out from the detail under it.
function Step($text) { Write-Host ""; Write-Host "  $text" -ForegroundColor Cyan }
function Note($text) { Write-Host "    $text" -ForegroundColor Gray }
function Good($text) { Write-Host "    $text" -ForegroundColor Green }
function Important($text) { Write-Host "    $text" -ForegroundColor Yellow }

# Callout frames the few things somebody has to act on - what to click in
# Docker's windows, the code to type into the first screen - so they cannot be
# lost among the progress lines scrolling past. A line starting with "*" is the
# thing itself (a code, an address) and is drawn in the frame's colour.
#
# ASCII only, like the rest of this file: it has no byte order mark, so
# Windows PowerShell reads it in the system code page, where box-drawing
# characters and dashes come out as mojibake.
function Callout([string]$Title, [string[]]$Lines, [ConsoleColor]$Color = 'Yellow') {
    Write-Host ""
    Write-Host ("  +--- " + $Title + " " + ('-' * [Math]::Max(4, 62 - $Title.Length))) -ForegroundColor $Color
    foreach ($line in $Lines) {
        Write-Host "  |  " -ForegroundColor $Color -NoNewline
        if ($line.StartsWith('*')) {
            Write-Host $line.Substring(1) -ForegroundColor $Color
        } else {
            Write-Host $line -ForegroundColor White
        }
    }
    Write-Host ("  +" + ('-' * 69)) -ForegroundColor $Color
    Write-Host ""
}

# Show-DockerGuide says what Docker Desktop is about to ask, before it asks.
#
# Its first start opens a window of its own - terms, then an offer to sign in or
# create an account, then a survey - in front of a setup that is waiting on it.
# Somebody who has never heard of Docker cannot tell which of those matter, or
# whether the account is needed (it is not), or whether closing the window
# breaks something (it does not). Shown once per run, whichever comes first of
# installing Docker (which opens itself when it finishes) or starting it.
$script:dockerGuideShown = $false
function Show-DockerGuide {
    if ($script:dockerGuideShown) { return }
    $script:dockerGuideShown = $true
    Callout 'Docker Desktop may open a window' @(
        'SoundStorm runs inside a free program called Docker. The first time',
        'it starts, Docker asks a few questions.',
        '*You do NOT need a Docker account.',
        '',
        '*  1. Subscription Service Agreement   ->  click Accept',
        '*  2. Sign in / create an account      ->  click Skip',
        '*  3. Questions about you or your work ->  click Skip',
        '',
        'No Skip button? Choose "Continue without signing in" instead.',
        '',
        'Then come back to THIS window. You can minimise or close the Docker',
        'window - Docker keeps running in the background, and this setup',
        'carries on by itself as soon as Docker is ready.'
    ) 'Cyan'
}

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
            # What was shown is kept, so a failure can be told apart by what
            # it said - a rate limit wants waiting out, not a new connection.
            $kept = New-Object System.Collections.Generic.List[string]
            # Which images are still coming, by name. The heartbeat used to
            # say only "still downloading...", right under the last image
            # that had *finished* - so that one looked like the slow one.
            # Somebody asked why Valkey, the smallest image of all, took for
            # ever.
            $pending = New-Object System.Collections.Generic.List[string]
            $total = 0
            & docker @Arguments 2>&1 | ForEach-Object {
                $line = "$_"
                if ($line -match '^\s*(?:Image\s+)?(\S+)\s+(Pulling|Pulled|Interrupted|Error)\s*$') {
                    $image = $Matches[1]
                    if ($Matches[2] -eq 'Pulling') {
                        if (-not $pending.Contains($image)) { $pending.Add($image); $total++ }
                    } else {
                        [void]$pending.Remove($image)
                    }
                }
                if (Test-DockerChurn $line) {
                    # Swallowed, but not silently: a download this long with
                    # nothing on screen is how somebody decides it has hung
                    # and closes the window.
                    if (((Get-Date) - $lastBeat).TotalSeconds -ge 30) {
                        if ($pending.Count -gt 0) {
                            # "jellyfin", not "jellyfin/jellyfin:latest".
                            $names = @($pending | ForEach-Object { ($_ -split '/')[-1] -replace ':.*$', '' })
                            Note "still downloading $($pending.Count) of ${total}: $($names -join ', ')"
                        } else {
                            Note "still downloading..."
                        }
                        $lastBeat = Get-Date
                    }
                    return
                }
                Write-Host $line
                $kept.Add($line)
                $lastBeat = Get-Date
            }
            return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = ($kept -join [Environment]::NewLine) }
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

# Get-Gateway is the home router's LAN address - the default route's next hop -
# so remote access can ask it to open the port (NAT-PMP/PCP). The container
# cannot find this itself, for the same reason it cannot find the LAN address:
# its own default route is the Docker bridge, not the router.
#
# The same private-range order as Get-LanAddress, because Docker's and WSL's
# virtual adapters have default routes of their own in the 172 range that reach
# nothing. A real gateway is on-link and never 0.0.0.0.
function Get-Gateway {
    try {
        $routes = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -AddressFamily IPv4 -ErrorAction Stop |
            Where-Object { $_.NextHop -and $_.NextHop -ne '0.0.0.0' } |
            Sort-Object RouteMetric, InterfaceMetric

        foreach ($pattern in @('192.168.*', '10.*', '172.*')) {
            $match = $routes | Where-Object { $_.NextHop -like $pattern } | Select-Object -First 1
            if ($match) { return $match.NextHop }
        }
        if ($routes) { return ($routes | Select-Object -First 1).NextHop }
    } catch {
        # Not worth a failed install; remote access just falls back to a manual
        # port-forward.
    }
    return $null
}

# Get-UpnpUrl discovers the router's UPnP device-description URL over SSDP, the
# fallback for opening the port when the router speaks UPnP but not NAT-PMP/PCP.
#
# Done here, on the host, because SSDP is multicast to 239.255.255.250 and that
# does not cross the Docker bridge into the container - the same reason the
# gateway is discovered here. The SOAP that uses this URL later is ordinary
# unicast and does work from the container.
function Get-UpnpUrl {
    $udp = $null
    try {
        $udp = New-Object System.Net.Sockets.UdpClient
        $udp.Client.ReceiveTimeout = 3000
        $dst = New-Object System.Net.IPEndPoint ([System.Net.IPAddress]::Parse('239.255.255.250')), 1900
        $msg = "M-SEARCH * HTTP/1.1`r`n" +
               "HOST: 239.255.255.250:1900`r`n" +
               "MAN: `"ssdp:discover`"`r`n" +
               "MX: 2`r`n" +
               "ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1`r`n`r`n"
        $bytes = [System.Text.Encoding]::ASCII.GetBytes($msg)
        [void]$udp.Send($bytes, $bytes.Length, $dst)

        $deadline = (Get-Date).AddSeconds(3)
        while ((Get-Date) -lt $deadline) {
            try {
                $from = New-Object System.Net.IPEndPoint ([System.Net.IPAddress]::Any), 0
                $data = $udp.Receive([ref]$from)
            } catch {
                break  # receive timeout: nothing more is coming
            }
            $text = [System.Text.Encoding]::ASCII.GetString($data)
            foreach ($line in ($text -split "`r`n")) {
                if ($line -match '(?i)^location:\s*(\S+)') {
                    return $Matches[1].Trim()
                }
            }
        }
    } catch {
        # UPnP is a best-effort fallback; NAT-PMP/PCP or a manual forward remain.
    } finally {
        if ($udp) { $udp.Close() }
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

# --- other devices on the network ---------------------------------------------
#
# Reaching SoundStorm from a phone was the one thing a laptop install could not
# do, and the installer only ever said "allow it through the firewall" in grey.
# Two things stand in the way, and neither is visible from the PC itself:
#
#   * Windows marks every new Wi-Fi network Public - the setting for cafes -
#     and a Public network lets nothing in.
#   * The first time Docker publishes a port, Windows asks whether "Docker
#     Desktop Backend" (com.docker.backend.exe, which is what accepts the
#     connections) may use networks. Its default ticks Private only, and
#     whatever is unticked - or everything, if the dialog is dismissed - gets
#     a Block rule, which beats any Allow.
#
# So after SoundStorm is running (and after that dialog has done whatever it
# did), the installer checks both and puts them right, with the person's say-so
# for anything that changes how Windows trusts a network. Only Private networks
# are ever opened: a network somebody has told Windows is their home, where the
# router already keeps the internet out unless they forward a port - which is
# exactly the case remote access needs this rule for.

$script:LanRuleName = 'SoundStorm - other devices on your home network'

# Get-LanProfile is the Windows network profile of the adapter holding Address:
# Category is Public, Private or DomainAuthenticated.
function Get-LanProfile([string]$Address) {
    try {
        $ip = Get-NetIPAddress -IPAddress $Address -ErrorAction Stop | Select-Object -First 1
        $network = Get-NetConnectionProfile -InterfaceIndex $ip.InterfaceIndex -ErrorAction Stop | Select-Object -First 1
        return [pscustomobject]@{
            Category       = [string]$network.NetworkCategory
            InterfaceIndex = [int]$ip.InterfaceIndex
            Name           = [string]$network.Name
        }
    } catch {
        return $null
    }
}

# Get-DockerPrivateBlocks lists the enabled inbound Block rules for Docker's
# listener that apply on Private networks - what a dismissed or default-answered
# firewall dialog leaves behind, and what would beat the Allow rule below.
function Get-DockerPrivateBlocks {
    try {
        return @(Get-NetFirewallApplicationFilter -ErrorAction Stop |
            Where-Object { $_.Program -like '*\com.docker.backend.exe' } |
            Get-NetFirewallRule -ErrorAction Stop |
            Where-Object {
                "$($_.Direction)" -eq 'Inbound' -and "$($_.Action)" -eq 'Block' -and
                "$($_.Enabled)" -eq 'True' -and "$($_.Profile)" -match 'Private|Any'
            })
    } catch {
        return @()
    }
}

# Test-LanAccessReady says whether a Private network already lets other devices
# in: SoundStorm's Allow rule is there for this port, and nothing blocks
# Docker's listener on Private. Readable without administrator, which is what
# keeps an update from asking for permission every time.
function Test-LanAccessReady([int]$Port) {
    try {
        $ours = @(Get-NetFirewallRule -DisplayName $script:LanRuleName -ErrorAction Stop |
            Where-Object { "$($_.Enabled)" -eq 'True' -and "$($_.Action)" -eq 'Allow' })
        $portOk = $false
        foreach ($rule in $ours) {
            if (@(($rule | Get-NetFirewallPortFilter).LocalPort) -contains "$Port") { $portOk = $true }
        }
        if (-not $portOk) { return $false }
    } catch {
        return $false
    }
    return (Get-DockerPrivateBlocks).Count -eq 0
}

# Enable-LanAccess makes the changes, in one elevated step: optionally mark the
# network Private, add SoundStorm's Allow rule for Port on Private networks,
# and take Private out of any Docker Block rule (leaving it blocking on Public,
# where it was). Returns the exit code, or $null when permission was refused.
#
# The script is passed encoded, and everything put into it is an integer or a
# fixed string, so nothing from the network reaches it as code.
function Enable-LanAccess([int]$Port, [int]$InterfaceIndex, [bool]$MakePrivate) {
    $makePrivateText = if ($MakePrivate) { '$true' } else { '$false' }
    $script = @"
`$ErrorActionPreference = 'Stop'
try {
    if ($makePrivateText) { Set-NetConnectionProfile -InterfaceIndex $InterfaceIndex -NetworkCategory Private }
    Get-NetFirewallRule -DisplayName '$($script:LanRuleName)' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName '$($script:LanRuleName)' ``
        -Description 'Lets phones, TVs and other computers on a network you have marked Private reach SoundStorm. Added by the SoundStorm setup.' ``
        -Direction Inbound -Action Allow -Protocol TCP -LocalPort $Port -Profile Private | Out-Null
    `$blocks = Get-NetFirewallApplicationFilter | Where-Object { `$_.Program -like '*\com.docker.backend.exe' } |
        Get-NetFirewallRule | Where-Object {
            "`$(`$_.Direction)" -eq 'Inbound' -and "`$(`$_.Action)" -eq 'Block' -and
            "`$(`$_.Enabled)" -eq 'True' -and "`$(`$_.Profile)" -match 'Private|Any' }
    foreach (`$rule in `$blocks) {
        `$profiles = "`$(`$rule.Profile)"
        `$keep = @()
        if (`$profiles -match 'Any|Domain') { `$keep += 'Domain' }
        if (`$profiles -match 'Any|Public') { `$keep += 'Public' }
        if (`$keep.Count) { Set-NetFirewallRule -Name `$rule.Name -Profile (`$keep -join ',') } else { Disable-NetFirewallRule -Name `$rule.Name }
    }
    exit 0
} catch {
    exit 1
}
"@
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($script))
    return Invoke-Elevated 'powershell.exe' @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', $encoded)
}

# Set-LanAccess checks, asks where it has to, fixes, and reports how it went:
# 'ready', 'public' (said it is not a home network), 'domain', 'refused'
# (permission declined), 'failed', or 'unknown' (no LAN address to judge by).
function Set-LanAccess([string]$Address, [int]$Port) {
    if (-not $Address) { return 'unknown' }
    $network = Get-LanProfile $Address
    if (-not $network) { return 'unknown' }

    if ($network.Category -eq 'DomainAuthenticated') {
        Note "This PC is on a work network, so SoundStorm does not open itself to other"
        Note "devices on it - that is for whoever runs the network to decide."
        return 'domain'
    }

    $makePrivate = $false
    if ($network.Category -eq 'Public') {
        Callout 'Is this your home network?' @(
            "Windows is treating the network this PC is on (""$($network.Name)"") as",
            'PUBLIC - the setting for cafes and airports - so it stops your phone,',
            'TV and other computers from reaching SoundStorm.',
            '',
            'If this is your own home network, SoundStorm can mark it as private.',
            'On a network you do not own, answer N and nothing is changed.'
        ) 'Cyan'
        $answer = ''
        try {
            $answer = Read-Host '    Is this your home network? Type Y or N, then press Enter'
        } catch {
            # No console to ask on: change nothing, which is the safe answer.
        }
        if ($answer -notmatch '^\s*y') {
            Note "Leaving this network as it is."
            return 'public'
        }
        $makePrivate = $true
    } elseif (Test-LanAccessReady $Port) {
        Good "Other devices on your network can reach SoundStorm."
        return 'ready'
    }

    Note "Letting other devices on your home network reach SoundStorm."
    if (-not (Test-Administrator)) { Important "Windows will ask for permission - click Yes." }
    $code = Enable-LanAccess $Port $network.InterfaceIndex $makePrivate
    if ($null -eq $code) {
        Important "Permission was not given, so other devices still cannot reach it."
        return 'refused'
    }
    if ($code -eq 0 -and (Test-LanAccessReady $Port)) {
        Good "Done - other devices on your network can reach SoundStorm."
        return 'ready'
    }
    Important "Could not change the network settings (code $code)."
    return 'failed'
}

# Update-LanAddress points the recorded LAN address at this machine's current
# one when it has moved - a laptop on another network, or a router that handed
# out a new address. The secure name follows SOUNDSTORM_TLS_HOSTS, so without
# this it kept pointing at an address this PC no longer has. Only the first
# entry, and only when it has gone from every adapter here: an address still on
# this machine was chosen, not left behind. Returns $true when it changed.
function Update-LanAddress {
    $hosts = Get-EnvSetting 'SOUNDSTORM_TLS_HOSTS'
    if (-not $hosts) { return $false }
    $parts = @($hosts -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    $recorded = $null
    if (-not $parts -or -not [Net.IPAddress]::TryParse($parts[0], [ref]$recorded)) { return $false }
    $current = Get-LanAddress
    if (-not $current -or $current -eq $parts[0]) { return $false }
    try {
        $mine = @(Get-NetIPAddress -ErrorAction Stop | ForEach-Object { $_.IPAddress })
    } catch {
        return $false
    }
    if ($mine -contains $parts[0]) { return $false }
    $parts[0] = $current
    Set-EnvSetting 'SOUNDSTORM_TLS_HOSTS' ($parts -join ',')
    return $true
}

# Show-LanAdvice ends the summary with what to do if a phone still cannot
# connect. This PC can check its own settings but cannot see what the phone
# sees, so it says what is left to check rather than claiming it works.
function Show-LanAdvice([string]$State) {
    switch ($State) {
        'ready' {
            Write-Host "  If a phone still cannot connect: it must be on the same Wi-Fi as" -ForegroundColor Gray
            Write-Host "  this PC - not mobile data, and not a 'guest' network, which keeps" -ForegroundColor Gray
            Write-Host "  devices apart on purpose." -ForegroundColor Gray
            Write-Host ""
        }
        { $_ -in 'public', 'refused', 'failed' } {
            Important "Other devices cannot reach SoundStorm yet."
            Write-Host "  To fix it later, on your home network, run 'Update SoundStorm' from" -ForegroundColor Gray
            Write-Host "  the Start menu and answer Y - or in Windows Settings, open" -ForegroundColor Gray
            Write-Host "  Network & internet, your Wi-Fi, and set 'Network profile type' to" -ForegroundColor Gray
            Write-Host "  Private." -ForegroundColor Gray
            Write-Host ""
        }
    }
}

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

    Note "Setting up Windows Subsystem for Linux - Docker runs on it, and it is"
    Note "missing or out of date on this PC."
    if (-not (Test-Administrator)) {
        Important "Windows will ask for permission - click Yes."
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
    # Docker opens itself the moment its installer finishes, so this is the
    # last chance to say what it is going to ask.
    Show-DockerGuide

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
        Important "Windows will ask for permission to install it - click Yes."
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

    Show-DockerGuide
    Note "Starting Docker Desktop. This takes a minute or two."
    Start-Process -FilePath $exe | Out-Null

    $waited = 0
    while (-not (Test-DockerRunning)) {
        Start-Sleep -Seconds 3
        $waited += 3
        if ($waited % 30 -eq 0) {
            Note "Docker is still starting... ($waited seconds). This is normal."
            # By a minute in, a window waiting on a click is the likeliest
            # reason, and the box that said what to click has scrolled away.
            if ($waited -eq 60) {
                Important "If a Docker window is waiting on you, see the box above:"
                Important "Accept the terms, and Skip the sign-in and the questions."
            }
        }
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
            if ($waited -eq 0) { Note "Waiting for SoundStorm to answer - usually under a minute." }
            Start-Sleep -Seconds 2
            $waited += 2
            # Up to three minutes with nothing on screen is exactly when
            # somebody decides it has hung and closes the window.
            if ($waited % 20 -eq 0) { Note "still starting... ($waited seconds). This is normal the first time." }
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

# Format-Size is a byte count the way Explorer shows one.
function Format-Size([double]$Bytes) {
    if ($Bytes -ge 1TB) { return ('{0:N1} TB' -f ($Bytes / 1TB)) }
    return ('{0:N0} GB' -f ($Bytes / 1GB))
}

# Select-LibraryLocation asks, on a first install, where the media should go,
# and returns the folder chosen - or $null to keep the default beside the
# install.
#
# -Library could always do this, but nothing ever asked, so only somebody who
# had read the README knew it was possible - and the library is the one part of
# this that outgrows a laptop's disk, where moving it later means moving every
# file. So the question comes before anything is put there, with the free space
# on each drive beside it, since that is what the answer turns on.
#
# A folder picker rather than a typed path: typing C:\Users\... is not
# something the person this is for should have to do. Typing still works, as
# the fallback when no dialog can be shown.
function Select-LibraryLocation([string]$Default) {
    $drives = @()
    try {
        # Fixed and removable drives with a letter. Network drives are left
        # out on purpose: Docker Desktop cannot see a mapped drive letter, so
        # offering one would be offering a library the media servers cannot
        # read.
        $drives = @(Get-CimInstance Win32_LogicalDisk -ErrorAction Stop |
            Where-Object { ($_.DriveType -eq 2 -or $_.DriveType -eq 3) -and $_.Size -gt 0 })
    } catch {
    }

    $lines = @(
        'Your music, films and books will be kept in:',
        "*  $Default",
        ''
    )
    if ($drives.Count -gt 1) {
        $lines += 'Free space on this PC:'
        foreach ($drive in $drives) {
            $label = if ($drive.VolumeName) { " ($($drive.VolumeName))" } else { '' }
            $lines += "   $($drive.DeviceID)$label  $(Format-Size $drive.FreeSpace) free of $(Format-Size $drive.Size)"
        }
        $lines += ''
    }
    $lines += @(
        'A film collection can need hundreds of GB. To keep it on another',
        'drive - an external one, say - choose a folder there now. Moving it',
        'later means moving every file.'
    )
    Callout 'Where should your library go?' $lines 'Cyan'

    $answer = ''
    try {
        $answer = Read-Host '    Press Enter to keep it there, or type C to choose another folder'
    } catch {
        return $null
    }
    if ($answer -notmatch '^\s*c') { return $null }

    $picked = $null
    try {
        Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop
        $dialog = New-Object System.Windows.Forms.FolderBrowserDialog
        $dialog.Description = 'Choose where SoundStorm keeps your music, films and books. A new folder called SoundStorm is made inside the one you pick.'
        $dialog.ShowNewFolderButton = $true
        # Owned by a topmost form, or the dialog opens behind this window and
        # the setup looks as if it has stopped.
        $owner = New-Object System.Windows.Forms.Form
        $owner.TopMost = $true
        try {
            if ($dialog.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
                $picked = $dialog.SelectedPath
            } else {
                Note "No folder chosen - keeping the library in $Default."
                return $null
            }
        } finally {
            $owner.Dispose()
            $dialog.Dispose()
        }
    } catch {
        try {
            $picked = Read-Host '    Type the folder to use, for example E:\Media'
        } catch {
            return $null
        }
    }
    if (-not $picked -or -not $picked.Trim()) { return $null }
    $picked = $picked.Trim().Trim('"')

    # A network location looks like any other folder in the picker, and the
    # media servers cannot read one: Docker Desktop does not see mapped drives
    # or \\server\share paths. Better said now than as an empty library later.
    $isNetwork = $picked.StartsWith('\\')
    if (-not $isNetwork -and $picked -match '^([A-Za-z]:)') {
        try {
            $disk = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='$($Matches[1].ToUpper())'" -ErrorAction Stop
            $isNetwork = ($disk.DriveType -eq 4)
        } catch {
        }
    }
    if ($isNetwork) {
        Important "That is a network location, which the media servers cannot read."
        Note "Keeping the library in $Default. Choose a drive plugged into this PC instead."
        return $null
    }

    # Its own folder inside whatever was picked. Somebody who picks E:\ does
    # not mean "scatter seven shelves across the root of my drive", and
    # somebody who picks an existing Media folder does not mean "mix these in
    # with what is there".
    if ([IO.Path]::GetFileName($picked.TrimEnd('\')) -ne 'SoundStorm') {
        $picked = Join-Path $picked 'SoundStorm'
    }
    return $picked
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
    Protect-SecretFile $envFile
}

# Protect-SecretFile makes a file readable by this user only - plus SYSTEM and
# Administrators, who can take ownership of anything anyway.
#
# .env holds the first sign-up's setup code and, with -Tailscale, a reusable
# auth key; the uninstaller's backup holds the password SoundStorm made on every
# media server. Left alone, a file inherits its folder's permissions. Under the
# user profile, the default, that already keeps other users out - but the
# install can live anywhere (SOUNDSTORM_DIR), and a folder like C:\SoundStorm
# inherits "Users: read" from the drive. install.sh gets the same protection
# from umask 077.
#
# Accounts are named by SID rather than by name, because group names are
# localized: "Administrators" is "Administratoren" on a German Windows, and a
# lookup by name would fail there. Best effort: a file that cannot be locked
# down still works, and refusing to install over it would be the worse failure.
function Protect-SecretFile([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    try {
        $acl = New-Object System.Security.AccessControl.FileSecurity
        # Protected, and inherited entries not copied: only the rules below.
        $acl.SetAccessRuleProtection($true, $false)
        $owners = @(
            [System.Security.Principal.WindowsIdentity]::GetCurrent().User,
            (New-Object System.Security.Principal.SecurityIdentifier 'S-1-5-18'),     # SYSTEM
            (New-Object System.Security.Principal.SecurityIdentifier 'S-1-5-32-544')  # Administrators
        )
        foreach ($sid in $owners) {
            $acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule(
                $sid, 'FullControl', 'Allow')))
        }
        # Not Set-Acl: in Windows PowerShell it writes every section of the
        # descriptor, the audit list included, and writing that needs
        # SeSecurityPrivilege - which an ordinary account does not hold. It
        # worked on the development machine and failed on a laptop with
        # "The process does not possess the 'SeSecurityPrivilege' privilege".
        # SetAccessControl writes only the sections that were changed here,
        # which is the permission list and nothing else.
        (Get-Item -LiteralPath $Path -Force).SetAccessControl($acl)
        return
    } catch {
        $firstError = $_.Exception.Message
    }
    # icacls is the second way to say the same thing, and names the accounts by
    # SID too, so it is not thrown by a localized "Administrators".
    $me = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $result = Invoke-Native 'icacls.exe' @($Path, '/inheritance:r', '/grant:r',
        "*${me}:F", '*S-1-5-18:F', '*S-1-5-32-544:F')
    if ($result.ExitCode -eq 0) { return }
    # Not fatal: the folder's own permissions still apply, and under the user
    # profile those already keep other accounts out. Said once, plainly.
    Note "Could not tighten the permissions on $([IO.Path]::GetFileName($Path)) ($firstError)."
    Note "It is still protected by the folder it is in; SoundStorm works normally."
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
# Get-HasAccount asks the running server whether the first account exists yet.
# $true or $false, or $null when it cannot tell - the setup code is only worth
# showing while nobody has signed up, since it is read by the first sign-up
# alone.
function Get-HasAccount([string]$Url) {
    $priorCallback = $null
    $bypassed = $false
    if ($Url -like 'https://*') {
        # A certificate this PC minted, on this PC; same reasoning as
        # Wait-ForSoundStorm.
        $priorCallback = [Net.ServicePointManager]::ServerCertificateValidationCallback
        [Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
        $bypassed = $true
    }
    try {
        $request = [Net.HttpWebRequest]::Create("$Url/api/session")
        $request.Timeout = 5000
        $response = $request.GetResponse()
        $reader = New-Object IO.StreamReader($response.GetResponseStream())
        $body = $reader.ReadToEnd()
        $response.Close()
        return [bool](($body | ConvertFrom-Json).hasAccount)
    } catch {
        return $null
    } finally {
        if ($bypassed) { [Net.ServicePointManager]::ServerCertificateValidationCallback = $priorCallback }
    }
}

# Format-SetupCode groups the code in fours so it can be read off the screen and
# typed. The server ignores case, spaces and dashes, so this is presentation.
function Format-SetupCode([string]$Code) {
    $plain = ($Code -replace '[-\s]', '').ToUpperInvariant()
    return (($plain -split '(.{4})' | Where-Object { $_ }) -join '-')
}

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
                # Written by the container through a bind mount, so it arrives
                # with the folder's permissions; it holds every media server's
                # password, so it gets this user's alone.
                Protect-SecretFile $backup
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
    # A laptop that moved to another network: point the secure name at where
    # it is now. compose sees the changed .env and recreates the container.
    $null = Update-LanAddress
    if ((Invoke-DockerBounded @('compose', 'up', '-d')) -ne 0) {
        Stop-With "  SoundStorm would not start.`n`n  Try turning the PC off and on again. If it keeps happening, show`n  this to whoever gave you the app:`n`n    cd `"$Dir`"; docker compose logs"
    }
    $port = Get-InstalledPort
    $url = "$(Get-InstalledScheme)://localhost:$port"
    Wait-ForSoundStorm $url
    # At startup there is nobody watching yet, so the browser stays shut; the
    # desktop icon is what opens it.
    if (-not $NoBrowser) {
        # With the setup code while nobody has signed up yet. Somebody who
        # closed the tab setup opened reaches for this icon next, and without
        # the code the first screen asks for one they have no idea where to
        # find.
        $open = $url
        $code = Get-EnvSetting 'SOUNDSTORM_SETUP_CODE'
        if ($code -and (Get-HasAccount $url) -ne $true) { $open = "$url/?setup=$code" }
        Start-Process $open
    }
    exit 0
}

# --- installing -----------------------------------------------------------------

Write-Host ""
Write-Host "  SoundStorm" -ForegroundColor White -NoNewline
Write-Host " - all your music, films, books and audiobooks in one place"
Write-Host "  -----------------------------------------------------------"

# Said before anything happens, because the two questions somebody has from
# here on are "is it still working?" and "what am I supposed to do?" - and on a
# first install the honest answer to the first is "for a while yet".
$firstInstall = -not (Test-Path (Join-Path $Dir 'docker-compose.yml'))
Write-Host ""
if ($firstInstall) {
    Write-Host "  This sets everything up by itself, in 4 steps. The first time takes" -ForegroundColor White
    Write-Host "  about 10 to 30 minutes, mostly downloading." -ForegroundColor White
    Write-Host ""
    Write-Host "  Keep this window open. It tells you when it is finished and exactly" -ForegroundColor Yellow
    Write-Host "  what to do next. You can use the computer while it works." -ForegroundColor Yellow
} else {
    Write-Host "  Updating SoundStorm. Your library, accounts and settings are kept." -ForegroundColor White
    Write-Host "  Keep this window open until it says it is finished." -ForegroundColor Yellow
}

Step "Step 1 of 4 - Getting Docker ready"
Initialize-Docker
Good "Docker is ready. ($((Invoke-Native 'docker' @('--version')).Output))"

Step "Step 2 of 4 - Preparing the SoundStorm folder"
Note $Dir

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
    Protect-SecretFile (Join-Path $Dir '.env')
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
if (Update-LanAddress) { Note "This PC's network address has changed; SoundStorm will use the new one." }
$tlsMode = Get-EnvSetting 'SOUNDSTORM_TLS'

# Remote access is off unless -Remote is given, and it can be turned on later
# from inside the app - so this only ever writes when the flag is present, and
# an existing choice (the app's, or a previous run's) is left alone otherwise.
# It needs auto https to have a real certificate; the toggle in the app is
# hidden without one, and the server refuses the change, so the flag just seeds
# the default that toggle starts from.
if ($Remote) {
    Set-EnvSetting 'SOUNDSTORM_REMOTE_ACCESS' 'on'
    Note "Turning on access from the internet."
    if ($tlsMode -ne 'auto') {
        Write-Host "  Note: reaching it from the internet needs https on." -ForegroundColor Yellow
    }
} elseif ($NoRemote) {
    Set-EnvSetting 'SOUNDSTORM_REMOTE_ACCESS' 'off'
    Note "Keeping it to the home network."
}

# The router address, for opening the port automatically when remote access is
# on. Written whether or not remote access is on yet, for the same reason as the
# LAN address: by the time somebody turns it on from inside the app, nothing on
# the host is running to work it out. An existing value is left alone, so a
# manual override stands.
if (-not (Get-EnvSetting 'SOUNDSTORM_GATEWAY')) {
    $gateway = Get-Gateway
    if ($gateway) { Set-EnvSetting 'SOUNDSTORM_GATEWAY' $gateway }
}

# The router's UPnP URL, the fallback for routers that do not speak NAT-PMP/PCP.
# Same reasoning as the gateway: discovered on the host, left alone if already
# set, best effort.
if (-not (Get-EnvSetting 'SOUNDSTORM_UPNP_URL')) {
    $upnp = Get-UpnpUrl
    if ($upnp) { Set-EnvSetting 'SOUNDSTORM_UPNP_URL' $upnp }
}

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

# Every run, not only when something above was written: an install from before
# this existed has a .env with its folder's permissions, and running the
# installer again - which is how updating works - is what fixes it.
Protect-SecretFile (Join-Path $Dir '.env')

# Where the library lives. Beside the install unless -Library says otherwise,
# which is how it goes on an external drive. Compose mounts every shelf from
# the same setting, so they all follow.
#
# Existing media is never moved for anybody: tens of gigabytes shifted by a
# script is exactly the operation that should not fail halfway. Somebody moving
# the library is told where the old files are, and how.
#
# On a first install with no -Library, the person is asked. Never on an update:
# the library already has media in it by then, and a question whose honest
# answer is "move every file yourself" is not one to ask on every update.
if (-not $Library -and $firstInstall -and -not (Get-EnvSetting 'SOUNDSTORM_LIBRARY_PATH')) {
    $choice = Select-LibraryLocation (Get-LibraryPath)
    if ($choice) { $Library = $choice }
}
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
        Write-Host "  To bring it across, close SoundStorm, move the folders inside it" -ForegroundColor Gray
        Write-Host "  into $full, and open SoundStorm again." -ForegroundColor Gray
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
    Step "Step 3 of 4 - Checking for a newer version"
} else {
    Step "Step 3 of 4 - Downloading the media servers"
    Note "About 8GB the first time. This is the long part - leave it running."
    Note "A line appears every half minute to show it is still going."
}
# Shown rather than captured: this is the part that takes minutes, and a
# silent window is how somebody decides it has hung.
#
# A registry that sheds load answers "toomanyrequests" - Docker Hub and ghcr
# both do, and eight images pulled at once is exactly what trips it. That is
# not the internet connection, and telling somebody to check theirs sends them
# the wrong way. So a failed pull is retried after a wait, which is what the
# registry asked for; everything already downloaded is kept between tries.
$pullWaits = @(30, 60, 120)
$pull = $null
for ($attempt = 0; $attempt -le $pullWaits.Count; $attempt++) {
    $pull = Invoke-Docker (@('compose') + $composeArgs + @('pull')) -Calm
    if ($pull.ExitCode -eq 0 -or $attempt -eq $pullWaits.Count) { break }
    $wait = $pullWaits[$attempt]
    if ($pull.Output -match 'toomanyrequests|too many requests|rate limit|\b429\b') {
        Important "The download server is busy and asked us to slow down."
    } else {
        Important "The download stopped part way."
    }
    Note "Trying again in $wait seconds - nothing already downloaded is lost."
    Start-Sleep -Seconds $wait
}
$rateLimited = $pull.Output -match 'toomanyrequests|too many requests|rate limit|\b429\b'
if ($pull.ExitCode -ne 0 -and $upgrade) {
    # An update that cannot download is not a broken install: the version
    # already here still works, so start that rather than stopping.
    Important "Could not check for a newer version right now."
    Note "Starting the version you already have. Run 'Update SoundStorm' again later."
} elseif ($pull.ExitCode -ne 0 -and $rateLimited) {
    Stop-With "  The download server is limiting how fast it hands out downloads.`n  Nothing is wrong with this PC or your internet connection.`n`n  Wait about half an hour and run the setup again - anything already`n  downloaded is kept."
} elseif ($pull.ExitCode -ne 0) {
    Stop-With "  Could not download the media servers. That is almost always the`n  internet connection. Try again - anything already downloaded is kept."
}

Step "Step 4 of 4 - Starting SoundStorm"
if ($firstInstall) {
    # The dialog appears the moment the port is first published, i.e. during
    # the next command, and its default answer is the one that shuts phones
    # out on a network Windows thinks is public.
    Callout 'Windows may ask about the firewall' @(
        'A "Windows Security Alert" may appear for "Docker Desktop Backend".',
        '*Click "Allow access".',
        '',
        'That is what lets your phone and TV reach SoundStorm. If you clicked',
        'Cancel by mistake, carry on - this setup checks it in a moment.'
    ) 'Cyan'
}
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
    Note "Adding shortcuts to the desktop and the Start menu."
    try {
        Install-Shortcuts
    } catch {
        # Not worth failing an otherwise finished install over.
        Note "Could not add shortcuts: $($_.Exception.Message)"
        Note "SoundStorm still works at $url"
    }
}

# The waiting happens before anything says "finished". It used to come after
# "Opening it now", which then sat for up to 45 seconds with nothing opening.
$lan = Get-LanAddress
$lanAccess = Set-LanAccess $lan ([int]$port)
$secure = ''
if ($tlsMode -eq 'auto') {
    Note "Finishing up: getting a secure address for phones and other devices."
    Note "This can take up to a minute."
    $secure = Get-SecureAddress $port
}

Write-Host ""
Write-Host "  ======================================================================" -ForegroundColor Green
if ($upgrade) {
    Write-Host "   UPDATED." -ForegroundColor Green -NoNewline
    Write-Host " SoundStorm is up to date and running." -ForegroundColor White
} else {
    Write-Host "   FINISHED." -ForegroundColor Green -NoNewline
    Write-Host " SoundStorm is installed and running." -ForegroundColor White
}
Write-Host "  ======================================================================" -ForegroundColor Green
Write-Host ""
Write-Host "  To add music, films or books: drag them onto the SoundStorm window, or"
Write-Host "  put them in the 'SoundStorm media' folder on your desktop."
Write-Host ""
if ($secure) {
    # The real certificate is in: this address works with no warning on any
    # device, and a phone can install the app from it.
    Write-Host "  On your phone, TV or another computer on this network:"
    Write-Host ""
    Write-Host "    $secure" -ForegroundColor White
    Write-Host ""
    if ($lan) {
        Write-Host "  If that does not load, your router is refusing the name - use" -ForegroundColor Gray
        Write-Host "  http://${lan}:$port instead. Same account either way." -ForegroundColor Gray
    }
    Write-Host "  Worth saving as a bookmark." -ForegroundColor Gray
    Write-Host ""
} elseif ($lan) {
    Write-Host "  On your phone, TV or another computer on this network:"
    Write-Host ""
    Write-Host "    ${scheme}://${lan}:$port" -ForegroundColor White
    Write-Host ""
    if ($tlsMode -eq 'auto') {
        Write-Host "  SoundStorm is still getting its secure address, and moves there" -ForegroundColor Gray
        Write-Host "  by itself when it has one." -ForegroundColor Gray
    }
    Write-Host "  Same account. Worth saving as a bookmark - and worth giving this" -ForegroundColor Gray
    Write-Host "  PC a fixed address in your router, or that number will change." -ForegroundColor Gray
    Write-Host ""
}
if ($lan) { Show-LanAdvice $lanAccess }
if ($useTailscale) {
    Note "Connecting to your tailnet."
    $tailnet = Get-TailnetURL
    Write-Host ""
    if ($tailnet) {
        Write-Host "  From anywhere, on any device signed into your tailnet:"
        Write-Host ""
        Write-Host "    $tailnet" -ForegroundColor White
        Write-Host ""
        Write-Host "  It works away from the house, with nothing forwarded on your" -ForegroundColor Gray
        Write-Host "  router." -ForegroundColor Gray
    } else {
        Write-Host "  Tailscale is starting but has not reported an address yet." -ForegroundColor Yellow
        Write-Host "  Check the Tailscale admin console, or run:" -ForegroundColor Gray
        Write-Host ""
        Write-Host "    docker logs soundstorm-tailscale" -ForegroundColor Gray
    }
    Write-Host ""
    Write-Host "  Every device that should reach it needs the Tailscale app and the" -ForegroundColor Gray
    Write-Host "  same account. There is no way around that part." -ForegroundColor Gray
    Write-Host ""
}

if ($tlsMode -eq 'self-signed') {
    # Said plainly and up front, because the alternative is somebody deciding
    # their own install is broken or unsafe. Nobody but this PC can vouch for a
    # certificate covering an address like 192.168.0.19, so the warning is
    # unavoidable without a real domain name - but it is fixable per device,
    # and that fix is the useful half of this message.
    Write-Host "  The first visit shows a certificate warning on every device." -ForegroundColor Yellow
    Write-Host "  That is expected: the certificate was made by this PC, and no" -ForegroundColor Gray
    Write-Host "  outside authority can vouch for a home network address." -ForegroundColor Gray
    Write-Host "  Choose Advanced, then continue." -ForegroundColor Gray
    Write-Host ""
    $caHost = if ($lan) { $lan } else { 'localhost' }
    Write-Host "  To stop it asking, open this on each device and install the"
    Write-Host "  certificate it downloads:"
    Write-Host ""
    Write-Host "    https://${caHost}:$port/ca.crt" -ForegroundColor White
    Write-Host ""
    Write-Host "  To go back to plain http, run the setup again with -NoHttps." -ForegroundColor Gray
    Write-Host ""
} elseif ($tlsMode -eq 'off') {
    Write-Host "  Run the setup again with -Https to encrypt the connection." -ForegroundColor Gray
    Write-Host ""
}
if (-not $NoShortcuts) {
    Write-Host "  Next time, click the SoundStorm icon on your desktop." -ForegroundColor Gray
    if (-not $NoAutoStart) {
        Write-Host "  It also starts by itself when you turn the PC on." -ForegroundColor Gray
    }
}
Write-Host ""

# The one thing to act on goes last, so it is what is on screen when the
# window stops scrolling - and it carries the setup code in full. The code
# used to travel only inside the address the browser was opened at, so
# somebody whose page lost it (or who closed the tab, or opened the desktop
# icon instead) was asked for a code nothing had ever shown them.
$hasAccount = Get-HasAccount $url
if ($hasAccount -ne $true -and $setupCode) {
    Callout 'NEXT: create your account' @(
        'Your web browser is opening SoundStorm now. On the first screen, choose',
        'a username and password - that is your account for SoundStorm.',
        '',
        'If the page asks for a SETUP CODE, type this one:',
        '',
        "*        $(Format-SetupCode $setupCode)",
        '',
        'Capitals and dashes do not matter.',
        '',
        "Browser did not open?  Go to:  $url",
        "The code is also saved in:     $(Join-Path $Dir '.env')"
    ) 'Yellow'
    # With the code in the address too, so the page usually fills it in by
    # itself; it takes it out of the address once it has it.
    Start-Process "$url/?setup=$setupCode"
} else {
    Callout 'NEXT: open SoundStorm' @(
        'Your web browser is opening SoundStorm now. Sign in as usual.',
        '',
        "Browser did not open?  Go to:  $url"
    ) 'Green'
    Start-Process $url
}
