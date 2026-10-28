@echo off
setlocal enabledelayedexpansion

set "SCRIPT_DIR=%~dp0"
pushd "%SCRIPT_DIR%.."
set "ROOT_DIR=%CD%"
popd

set "SRC_DIR=%SCRIPT_DIR%LlmLimitsOverlay"
set "BIN_DIR=%SCRIPT_DIR%bin"
set "TARGET_EXE=%BIN_DIR%\LlmLimitsOverlay.exe"

set "CSC=C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe"
if not exist "%CSC%" (
    set "CSC=C:\Windows\Microsoft.NET\Framework\v4.0.30319\csc.exe"
)

if not exist "%CSC%" (
    echo [ERROR] csc.exe compiler not found at %CSC%
    exit /b 1
)

if not exist "%BIN_DIR%" (
    mkdir "%BIN_DIR%"
)

set "WPF_DIR=C:\Windows\Microsoft.NET\Framework64\v4.0.30319\WPF"
if not exist "%WPF_DIR%" (
    set "WPF_DIR=C:\Windows\Microsoft.NET\Framework\v4.0.30319\WPF"
)

echo =======================================================
echo Building LlmLimitsOverlay via C# Compiler (csc.exe)...
echo Target Framework: .NET Framework 4.8
echo Output: %TARGET_EXE%
echo =======================================================

"%CSC%" /nologo /t:winexe /platform:anycpu /optimize+ /win32icon:"%ROOT_DIR%\assets\app.ico" /resource:"%ROOT_DIR%\assets\app.ico",LlmLimitsOverlay.app.ico /out:"%TARGET_EXE%" /lib:"%WPF_DIR%" /r:PresentationFramework.dll,PresentationCore.dll,WindowsBase.dll,System.Xaml.dll "%SRC_DIR%\*.cs"

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] GUI build failed with exit code %ERRORLEVEL%.
    exit /b %ERRORLEVEL%
)

for %%F in ("%TARGET_EXE%") do (
    echo [SUCCESS] GUI build succeeded!
    echo Executable: %%~fF
    echo File size:  %%~zF bytes
)

echo =======================================================
echo Building llm-limits CLI via C# Compiler (csc.exe)...
echo Output: %BIN_DIR%\llm-limits.exe
echo =======================================================

set "CLI_TARGET_EXE=%BIN_DIR%\llm-limits.exe"
"%CSC%" /nologo /t:exe /platform:anycpu /optimize+ /out:"%CLI_TARGET_EXE%" /r:System.Core.dll,System.dll "%SCRIPT_DIR%LlmLimitsCli\Program.cs" "%SRC_DIR%\SimpleJson.cs" "%SRC_DIR%\Logger.cs" "%SRC_DIR%\EnvParser.cs" "%SRC_DIR%\AgyResolver.cs" "%SRC_DIR%\Config.cs" "%SRC_DIR%\Models.cs" "%SRC_DIR%\QuotaFetcher.cs"

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] CLI build failed with exit code %ERRORLEVEL%.
    exit /b %ERRORLEVEL%
)

for %%F in ("%CLI_TARGET_EXE%") do (
    echo [SUCCESS] CLI build succeeded!
    echo Executable: %%~fF
    echo File size:  %%~zF bytes
)

exit /b 0
