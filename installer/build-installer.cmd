@echo off
setlocal enabledelayedexpansion

rem Build LlmLimitsOverlay installer via Go and Inno Setup (ISCC.exe)
rem Output: dist/LlmLimitsOverlay-Setup-2.1.7.exe

set "SCRIPT_DIR=%~dp0"
pushd "%SCRIPT_DIR%\.."
set "ROOT_DIR=%CD%"
popd

set "GO_DIST_DIR=%ROOT_DIR%\go\dist"
if not exist "%GO_DIST_DIR%" (
    mkdir "%GO_DIST_DIR%"
)

set "DIST_DIR=%ROOT_DIR%\dist"
if not exist "%DIST_DIR%" (
    mkdir "%DIST_DIR%"
)

echo =======================================================
echo Building Go binaries (GUI + CLI)...
echo =======================================================

set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"

pushd "%ROOT_DIR%\go"
go build -ldflags="-s -w -H windowsgui" -o dist\llm-limits-overlay.exe .\cmd\overlay32
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Failed to build llm-limits-overlay.exe
    popd
    exit /b %ERRORLEVEL%
)

go build -ldflags="-s -w" -o dist\llm-limits.exe .\cmd\llm-limits
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Failed to build llm-limits.exe
    popd
    exit /b %ERRORLEVEL%
)
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

echo =======================================================
echo Building LlmLimitsOverlay Installer via Inno Setup...
echo Version: 2.1.7
echo Output: %DIST_DIR%\LlmLimitsOverlay-Setup-2.1.7.exe
echo =======================================================

"%ISCC%" "%ISS_FILE%"

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Installer build failed with exit code %ERRORLEVEL%.
    exit /b %ERRORLEVEL%
)

for %%F in ("%DIST_DIR%\LlmLimitsOverlay-Setup-2.1.7.exe") do (
    echo [SUCCESS] Installer built!
    echo Executable: %%~fF
    echo File size:  %%~zF bytes
)

exit /b 0