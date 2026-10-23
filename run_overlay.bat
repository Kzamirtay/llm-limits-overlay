@echo off
rem LLM Limits Overlay - Silent Launcher
rem Delegates to run_overlay.vbs via wscript.exe to prevent visible console window.
start "" wscript.exe //nologo "%~dp0run_overlay.vbs"
