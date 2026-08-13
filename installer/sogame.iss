#define MyAppName "SoGame"
#define MyAppVersion "1.3"
#define MyAppPublisher "vir_dominary"
#define MyAppExeName "SoGame.exe"
#define NetBirdMSI "netbird_installer_0.74.7_windows_amd64.msi"

[Setup]
AppId={{D3A6F4A0-1234-4F00-ABCD-000000000001}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL=https://github.com/vir-dominary
AppSupportURL=https://github.com/vir-dominary
AppUpdatesURL=https://github.com/vir-dominary
VersionInfoCompany={#MyAppPublisher}
VersionInfoDescription={#MyAppName}
DefaultDirName={autopf}\SoGame
DefaultGroupName={#MyAppName}
OutputDir=output
OutputBaseFilename=SoGame-Setup-{#MyAppVersion}
Compression=lzma
SolidCompression=yes
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog
DisableProgramGroupPage=yes
WizardStyle=modern
SetupIconFile=..\build\windows\icon.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
UninstallDisplayName={#MyAppName}
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
ShowLanguageDialog=no

[Languages]
Name: "chinese"; MessagesFile: "compiler:Default.isl"

[Messages]
chinese.WelcomeLabel2=这将安装 {#MyAppName} 到您的计算机。%n%n建议关闭其他应用程序后再继续。
chinese.SelectDirLabel3=安装程序将把 {#MyAppName} 安装到以下文件夹。
chinese.SelectDirBrowseLabel=如需安装到其他文件夹，请点击"浏览"。
chinese.InstallingLabel=正在安装 {#MyAppName}，请稍候...
chinese.FinishedHeadingLabel=安装完成
chinese.FinishedLabelNoIcons={#MyAppName} 已成功安装到您的计算机。
chinese.FinishedRestartLabel=要完成安装，需要重新启动计算机。是否立即重启？
chinese.ConfirmUninstall=确定要卸载 {#MyAppName} 吗？

[Files]
; 主程序
Source: "..\build\bin\SoGame.exe"; DestDir: "{app}"; Flags: ignoreversion
; 经典模式：n2n edge
Source: "..\bin\edge.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
; 经典模式：TAP 网卡驱动
Source: "tap\OemWin2k.inf"; DestDir: "{app}\tap"; Flags: ignoreversion
Source: "tap\tap0901.cat"; DestDir: "{app}\tap"; Flags: ignoreversion
Source: "tap\tap0901.sys"; DestDir: "{app}\tap"; Flags: ignoreversion
Source: "tap\tapinstall.exe"; DestDir: "{app}\tap"; Flags: ignoreversion
; 极速模式：NetBird 守护进程安装辅助程序（UAC 提权安装 MSI）
Source: "..\build\bin\sogame-helper.exe"; DestDir: "{app}"; Flags: ignoreversion
; 极速模式：官方 NetBird MSI（首次使用极速模式时由 sogame-helper 提权安装为系统服务）
Source: "..\bin\{#NetBirdMSI}"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加选项:"; Flags: checkedonce

[Code]

function IsTapInstalled(): Boolean;
var
  ResultCode: Integer;
  Output: string;
begin
  Result := False;
  if Exec('powershell', '-Command "Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -match ''tap0901|TAP-Windows'' } | Measure-Object | Select-Object -ExpandProperty Count"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    if (ResultCode = 0) and (Output <> '') and (StrToIntDef(Output, 0) > 0) then
      Result := True;
  end;
end;

function ShouldInstallTap(): Boolean;
begin
  Result := not IsTapInstalled();
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
begin
  if (CurStep = ssPostInstall) then
  begin
    Log('Installation completed. TAP driver and NetBird MSI are handled by the application at runtime.');

    // 清理旧版 WireGuard 防火墙规则（极速模式已改用 NetBird，不再使用 51820 端口）
    Exec('netsh', 'advfirewall firewall delete rule name="SoGame WireGuard"',
         '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  ConfigDir: string;
  KeyDir: string;
begin
  if CurUninstallStep = usUninstall then
  begin
    // 清理旧版防火墙规则
    Exec('netsh', 'advfirewall firewall delete rule name="SoGame WireGuard"',
         '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

    if MsgBox('是否删除用户配置文件和密钥？', mbConfirmation, MB_YESNO) = IDYES then
    begin
      ConfigDir := ExpandConstant('{userappdata}\SoGame');
      if DirExists(ConfigDir) then
        DelTree(ConfigDir, True, True, True);
      KeyDir := ExpandConstant('{localappdata}\SoGame');
      if DirExists(KeyDir) then
        DelTree(KeyDir, True, True, True);
    end;
  end;
end;
