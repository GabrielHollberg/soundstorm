@echo off
rem SoundStorm setup for Windows.
rem
rem This file exists to be double-clicked. Everything it needs to do is in
rem PowerShell, but asking somebody to open PowerShell and paste a command is
rem the step that loses people - a .cmd file runs from the Downloads folder
rem with one click and no terminal to find first.
rem
rem -ExecutionPolicy Bypass applies to this one process only. It is what makes
rem a downloaded script run at all on a default Windows install; it changes
rem nothing about the machine and nothing after this window closes.

setlocal

set "SCRIPT=%~dp0install.ps1"
if exist "%SCRIPT%" goto :local

rem Downloaded on its own: fetch the installer it belongs to.
if "%SOUNDSTORM_REPO%"=="" set "SOUNDSTORM_REPO=GabrielHollberg/soundstorm"
if "%SOUNDSTORM_BRANCH%"=="" set "SOUNDSTORM_BRANCH=main"
set "URL=https://raw.githubusercontent.com/%SOUNDSTORM_REPO%/%SOUNDSTORM_BRANCH%/install.ps1"

powershell -NoProfile -ExecutionPolicy Bypass -Command "iex ((New-Object Net.WebClient).DownloadString('%URL%'))"
goto :done

:local
powershell -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%"

:done
echo.
echo Press any key to close this window.
pause >nul
