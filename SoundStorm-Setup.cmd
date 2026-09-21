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

setlocal

set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
set "LOCAL=%~dp0install.ps1"
if exist "%LOCAL%" (
    "%PS%" -NoProfile -ExecutionPolicy Bypass -File "%LOCAL%" %*
    goto :done
)

if "%SOUNDSTORM_REPO%"=="" set "SOUNDSTORM_REPO=GabrielHollberg/soundstorm"
if "%SOUNDSTORM_BRANCH%"=="" set "SOUNDSTORM_BRANCH=main"
set "URL=https://raw.githubusercontent.com/%SOUNDSTORM_REPO%/%SOUNDSTORM_BRANCH%/install.ps1"
set "SAVED=%TEMP%\soundstorm-install.ps1"

echo.
echo   Fetching the SoundStorm installer...

rem curl.exe has shipped with Windows since 10 build 1803.
curl.exe -fsSL "%URL%" -o "%SAVED%" 2>nul

if not exist "%SAVED%" (
    rem Older Windows, or no curl: have PowerShell save it. Still to a file -
    rem what must not happen is running it straight out of memory.
    "%PS%" -NoProfile -ExecutionPolicy Bypass -Command "Invoke-WebRequest -Uri '%URL%' -OutFile '%SAVED%' -UseBasicParsing"
)

if not exist "%SAVED%" (
    echo.
    echo   Could not download the installer from:
    echo     %URL%
    echo.
    echo   Check the internet connection and try again.
    goto :done
)

"%PS%" -NoProfile -ExecutionPolicy Bypass -File "%SAVED%" %*
del "%SAVED%" >nul 2>&1

:done
echo.
echo Press any key to close this window.
pause >nul
