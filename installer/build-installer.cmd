@echo off
setlocal enabledelayedexpansion

rem Build LlmLimitsOverlay installer via Inno Setup (ISCC.exe)
rem Output: dist/LlmLimitsOverlay-Setup-1.4.1.exe

set "SCRIPT_DIR=%~dp0"
pushd "%SCRIPT_DIR%\.."
set "ROOT_DIR=%CD%"
popd

set "ISCC=%LOCALAPPDATA%\Programs\Inno Setup 6\ISCC.exe"
if not exist "%ISCC%" (
    set "ISCC=C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
)
if not exist "%ISCC%" (
    echo [ERROR] ISCC.exe not found. Install Inno Setup 6 first.
    echo Expected at: %ISCC%
    exit /b 1
)

set "ISS_FILE=%SCRIPT_DIR%\LlmLimitsOverlay.iss"
set "DIST_DIR=%ROOT_DIR%\dist"

if not exist "%DIST_DIR%" (
    mkdir "%DIST_DIR%"
)

echo =======================================================
echo Building LlmLimitsOverlay Installer via Inno Setup...
echo Version: 1.4.1
echo Output: %DIST_DIR%\LlmLimitsOverlay-Setup-1.4.1.exe
echo =======================================================

"%ISCC%" "%ISS_FILE%"

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Installer build failed with exit code %ERRORLEVEL%.
    exit /b %ERRORLEVEL%
)

for %%F in ("%DIST_DIR%\LlmLimitsOverlay-Setup-1.4.1.exe") do (
    echo [SUCCESS] Installer built!
    echo Executable: %%~fF
    echo File size:  %%~zF bytes
)

exit /b 0