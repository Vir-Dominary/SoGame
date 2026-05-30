@echo off
setlocal enabledelayedexpansion

net session >nul 2>&1
if errorlevel 1 (
    echo ERROR: Administrator privileges required
    exit /b 1
)

echo INFO: Checking for TAP adapter...

powershell -NoProfile -Command "$adapters = Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -like '*TAP*' -or $_.InterfaceDescription -like '*Wintun*' -or $_.InterfaceDescription -like '*tun*' }; if ($adapters) { exit 0 } else { exit 1 }" >nul 2>&1
if not errorlevel 1 (
    echo INFO: TAP adapter already available
    exit /b 0
)

set SCRIPT_DIR=%~dp0

set TAPINSTALL=
if exist "%SCRIPT_DIR%tapinstall.exe" set TAPINSTALL=%SCRIPT_DIR%tapinstall.exe
if exist "%SCRIPT_DIR%devcon.exe" set TAPINSTALL=%SCRIPT_DIR%devcon.exe
if not defined TAPINSTALL if exist "C:\Program Files\TAP-Windows\bin\tapinstall.exe" set TAPINSTALL=C:\Program Files\TAP-Windows\bin\tapinstall.exe
if not defined TAPINSTALL if exist "C:\Program Files\OpenVPN\bin\tapinstall.exe" set TAPINSTALL=C:\Program Files\OpenVPN\bin\tapinstall.exe

if not defined TAPINSTALL (
    echo ERROR: No TAP installation tool found
    echo ERROR: Expected tapinstall.exe or devcon.exe in %SCRIPT_DIR%
    exit /b 1
)

echo INFO: Adding TAP driver to driver store with pnputil...
pnputil /add-driver "%SCRIPT_DIR%OemWin2k.inf" /install
if errorlevel 1 (
    echo WARN: pnputil add-driver failed, trying with force flag...
    pnputil /add-driver "%SCRIPT_DIR%OemWin2k.inf" /install /force
)

echo INFO: Creating TAP adapter instance with %TAPINSTALL%...
"%TAPINSTALL%" install "%SCRIPT_DIR%OemWin2k.inf" tap0901
if errorlevel 1 (
    echo WARN: tapinstall failed with standard method, trying legacy method...
    rem Try using pnputil to install the driver first, then create instance
    pnputil /add-driver "%SCRIPT_DIR%OemWin2k.inf" /install 2>nul
    timeout /t 2 /nobreak >nul
    "%TAPINSTALL%" install "%SCRIPT_DIR%OemWin2k.inf" tap0901
    if errorlevel 1 (
        echo ERROR: TAP adapter installation failed after retry
        echo ERROR: This may be due to driver signing requirements.
        echo ERROR: Try running: bcdedit /set nointegritychecks on
        exit /b 1
    )
)

timeout /t 3 /nobreak >nul

powershell -NoProfile -Command "$adapters = Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -like '*TAP*' -or $_.InterfaceDescription -like '*Wintun*' -or $_.InterfaceDescription -like '*tun*' }; if ($adapters) { exit 0 } else { exit 1 }" >nul 2>&1
if not errorlevel 1 (
    echo SUCCESS: TAP adapter installed and verified
    exit /b 0
)

echo WARNING: TAP adapter installation completed but verification failed
exit /b 0
