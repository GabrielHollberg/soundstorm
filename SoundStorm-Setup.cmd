@echo off
rem SoundStorm setup for Windows.
rem
rem This file exists to be double-clicked. Everything it needs to do is in
rem PowerShell, but asking somebody to open PowerShell and paste a command is
rem the step that loses people - a .cmd file runs from the Downloads folder
rem with one click and no terminal to find first.
rem
rem It downloads the installer to a file and runs the file, rather than piping
rem it into PowerShell. That is not a style preference: the one-liner form,
rem
rem   powershell -Command "iex ((New-Object Net.WebClient).DownloadString(...))"
rem
rem is the canonical malware download cradle, and Windows Defender blocks it on
rem sight. It did exactly that here - "Access is denied", no explanation, on the
rem first thing a new user touches. Fetching to disk and running the file is
rem what ordinary installers do and what droppers avoid.
rem
rem -ExecutionPolicy Bypass applies to this one process only. It is what makes
rem a downloaded script run at all on a default Windows install; it changes
rem nothing about the machine and nothing after this window closes.
rem
rem Arguments are passed straight through, so a typed command or a shortcut
rem can say SoundStorm-Setup.cmd --https and have it reach the installer.
rem
rem No console stays open. A double-clicked .cmd always gets one - Windows
rem gives it no choice - so this one says nothing and does nothing but start
rem PowerShell minimised and hidden, then closes: a blink.

setlocal

set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
set "LOCAL=%~dp0install.ps1"
if exist "%LOCAL%" (
    start "" /min "%PS%" -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "%LOCAL%" %*
    exit /b 0
)

if "%SOUNDSTORM_REPO%"=="" set "SOUNDSTORM_REPO=GabrielHollberg/soundstorm"
if "%SOUNDSTORM_BRANCH%"=="" set "SOUNDSTORM_BRANCH=main"
set "SOUNDSTORM_SETUP_URL=https://raw.githubusercontent.com/%SOUNDSTORM_REPO%/%SOUNDSTORM_BRANCH%/install.ps1"
set "SOUNDSTORM_SETUP_ARGS=%*"

rem The download happens in the hidden PowerShell, not here, so this console
rem closes the moment PowerShell has started instead of waiting for it. Two
rem steps, as before: save the installer to a file, then start that file as
rem its own process - never run it straight out of memory, which is what
rem Defender blocks. A failed download says so in a message box, because
rem there is no console left to say it in. SOUNDSTORM_WINDOW tells the
rem installer it is already hidden, so it opens its window without first
rem relaunching itself.
start "" /min "%PS%" -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -Command "$ProgressPreference = 'SilentlyContinue'; [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; $f = Join-Path $env:TEMP 'soundstorm-install.ps1'; try { Invoke-WebRequest -UseBasicParsing -Uri $env:SOUNDSTORM_SETUP_URL -OutFile $f } catch { Add-Type -AssemblyName System.Windows.Forms; [void][System.Windows.Forms.MessageBox]::Show('SoundStorm could not download its installer. Check the internet connection and try again.', 'SoundStorm Setup'); exit 1 }; $env:SOUNDSTORM_WINDOW = '1'; $q = [char]34; Start-Process -FilePath (Join-Path $PSHOME 'powershell.exe') -WindowStyle Hidden -ArgumentList ('-NoProfile -ExecutionPolicy Bypass -STA -File ' + $q + $f + $q + ' ' + $env:SOUNDSTORM_SETUP_ARGS)"
exit /b 0
