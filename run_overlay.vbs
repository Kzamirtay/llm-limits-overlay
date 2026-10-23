' LLM Limits Overlay - Silent Background Launcher
' Starts powershell.exe with SW_HIDE (0) so no console window ever flashes or appears.
Option Explicit
Dim objShell, objFSO, strScriptDir, strPsScript, strCommand

Set objFSO = CreateObject("Scripting.FileSystemObject")
strScriptDir = objFSO.GetParentFolderName(WScript.ScriptFullName)
strPsScript = strScriptDir & "\limits_overlay.ps1"
If Not objFSO.FileExists(strPsScript) Then
    strPsScript = strScriptDir & "\agy_limits_overlay.ps1"
End If

Set objShell = CreateObject("WScript.Shell")
strCommand = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File """ & strPsScript & """"
objShell.Run strCommand, 0, False
