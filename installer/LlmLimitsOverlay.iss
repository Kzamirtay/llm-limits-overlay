; LlmLimitsOverlay Inno Setup Script
; Build: ISCC.exe LlmLimitsOverlay.iss
; Output: dist/LlmLimitsOverlay-Setup-1.3.0.exe

#define MyAppName "LLM Limits Overlay"
#define MyAppVersion "1.3.0"
#define MyAppPublisher "Kzamirtay"
#define MyAppURL "https://github.com/Kzamirtay/llm-limits-overlay"
#define MyAppExeName "LlmLimitsOverlay.exe"
#define MyAppId "{{8F3E7C2A-4B1D-4E9A-8C5F-2D6B9A1E4F7C}"

[Setup]
AppId={#MyAppId}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={localappdata}\LlmLimitsOverlay
DefaultGroupName={#MyAppName}
DisableDirPage=yes
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir=..\dist
OutputBaseFilename=LlmLimitsOverlay-Setup-{#MyAppVersion}
Compression=lzma
SolidCompression=yes
WizardStyle=modern
SetupIconFile=..\assets\app.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
UninstallDisplayName={#MyAppName} {#MyAppVersion}
RestartIfNeededByRun=false
CloseApplications=force
CloseApplicationsFilter={#MyAppExeName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "autostart"; Description: "Автозапуск при входе в Windows (Run в HKCU)"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "..\dotnet\bin\LlmLimitsOverlay.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dotnet\bin\llm-limits.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\overlay.config.json"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\assets\app.ico"; DestDir: "{app}\assets"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.ru.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\.env.example"; DestDir: "{app}"; Flags: ignoreversion
; .env — только если не существует, при удалении не трогаем
Source: "..\.env.example"; DestDir: "{app}"; DestName: ".env"; Flags: ignoreversion onlyifdoesntexist uninsneveruninstall

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; Comment: "Показать квоты LLM в оверлее"
Name: "{userdesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; Comment: "Показать квоты LLM в оверлее"; Tasks: desktopicon

[Registry]
; Автозапуск — HKCU\Software\Microsoft\Windows\CurrentVersion\Run
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "LlmLimitsOverlay"; ValueData: """{app}\{#MyAppExeName}"""; Flags: uninsdeletevalue; Tasks: autostart

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#MyAppName}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Удаляем data-файлы, но не .env
Type: files; Name: "{app}\limits*.json"
Type: files; Name: "{app}\overlay.log"

[Code]
function InitializeSetup(): Boolean;
begin
  Result := True;
end;