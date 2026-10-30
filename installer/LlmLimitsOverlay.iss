; LlmLimitsOverlay Inno Setup Script
; Build: ISCC.exe LlmLimitsOverlay.iss
; Output: dist/LlmLimitsOverlay-Setup-1.4.0.exe

#define MyAppName "LLM Limits Overlay"
#define MyAppVersion "1.4.0"
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
ChangesEnvironment=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "autostart"; Description: "Автозапуск при входе в Windows (Run в HKCU)"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "addtopath"; Description: "Добавить llm-limits.exe в PATH пользователя (запуск из любой папки)"; GroupDescription: "Параметры командной строки:";

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
// ---- User PATH management for CLI (addtopath task) --------------------
// Запись: HKCU\Environment, значение Path, REG_EXPAND_SZ.
// Добавляем/удаляем ТОЛЬКО свой {app}; сравнение без учёта регистра и
// хвостовых '\'. Пустых сегментов (";;") не оставляем.

function NormalizePathEntry(const S: string): string;
var
  R: string;
begin
  R := Lowercase(Trim(S));
  while (Length(R) > 0) and (R[Length(R)] = '\') do
    Delete(R, Length(R), 1);
  Result := R;
end;

function SplitPathRaw(const S: string): TArrayOfString;
var
  Start, I, Count: Integer;
begin
  Count := 0;
  Start := 1;
  for I := 1 to Length(S) do
  begin
    if S[I] = ';' then
    begin
      SetArrayLength(Result, Count + 1);
      Result[Count] := Copy(S, Start, I - Start);
      Count := Count + 1;
      Start := I + 1;
    end;
  end;
  SetArrayLength(Result, Count + 1);
  Result[Count] := Copy(S, Start, Length(S) - Start + 1);
end;

function NeedsAddPath(const Dir: string): Boolean;
var
  OrigPath, NormDir, Entry: string;
  Parts: TArrayOfString;
  I: Integer;
begin
  Result := True;
  if not RegQueryStringValue(HKCU, 'Environment', 'Path', OrigPath) then
    exit; // ключа нет — добавлять

  NormDir := NormalizePathEntry(Dir);
  Parts := SplitPathRaw(OrigPath);
  for I := 0 to GetArrayLength(Parts) - 1 do
  begin
    Entry := NormalizePathEntry(Parts[I]);
    if Entry = NormDir then
    begin
      Result := False;
      exit;
    end;
  end;
end;

procedure AddAppToPath;
var
  OrigPath: string;
begin
  if not NeedsAddPath(ExpandConstant('{app}')) then
    exit; // уже есть — дубль не добавляем

  if RegQueryStringValue(HKCU, 'Environment', 'Path', OrigPath) and (Length(OrigPath) > 0) then
    RegWriteExpandStringValue(HKCU, 'Environment', 'Path', OrigPath + ';' + ExpandConstant('{app}'))
  else
    RegWriteExpandStringValue(HKCU, 'Environment', 'Path', ExpandConstant('{app}'));
end;

procedure RemoveAppFromPath;
var
  OrigPath, NormDir, Entry, NewPath: string;
  Parts: TArrayOfString;
  I: Integer;
begin
  if not RegQueryStringValue(HKCU, 'Environment', 'Path', OrigPath) then
    exit;

  NormDir := NormalizePathEntry(ExpandConstant('{app}'));
  NewPath := '';
  Parts := SplitPathRaw(OrigPath);
  for I := 0 to GetArrayLength(Parts) - 1 do
  begin
    Entry := Trim(Parts[I]);
    if Length(Entry) = 0 then
      continue; // пропускаем пустые сегменты — ";;" не оставляем
    if NormalizePathEntry(Entry) = NormDir then
      continue; // убираем только своё вхождение
    if Length(NewPath) > 0 then
      NewPath := NewPath + ';' + Entry
    else
      NewPath := Entry;
  end;

  if NewPath <> OrigPath then
    RegWriteExpandStringValue(HKCU, 'Environment', 'Path', NewPath);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    if WizardIsTaskSelected('addtopath') then
      AddAppToPath;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveAppFromPath;
end;