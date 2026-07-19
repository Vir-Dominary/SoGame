#define MyAppName "SoGame"
#define MyAppVersion "1.3"
#define MyAppPublisher "vir_dominary"
#define MyAppExeName "SoGame.exe"

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
CloseApplications=force

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
; 主程序（wails build 输出到 build/bin/）
Source: "..\build\bin\SoGame.exe"; DestDir: "{app}"; Flags: ignoreversion
; n2n edge 二进制（位于项目根 bin/）
Source: "..\bin\edge.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
; TAP-Windows 驱动文件（随程序分发，运行时按需安装）
Source: "tap\amd64\OemVista.inf"; DestDir: "{app}\tap\amd64"; Flags: ignoreversion
Source: "tap\amd64\tap0901.cat"; DestDir: "{app}\tap\amd64"; Flags: ignoreversion
Source: "tap\amd64\tap0901.sys"; DestDir: "{app}\tap\amd64"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加选项:"; Flags: checkedonce
Name: "runafterinstall"; Description: "安装完成后启动 {#MyAppName}"; GroupDescription: "其他选项:"; Flags: checkedonce

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "启动 {#MyAppName}"; Tasks: runafterinstall; Flags: nowait postinstall skipifsilent

[Code]

// 安装前关闭正在运行的 SoGame，避免文件被占用导致安装失败
function PrepareToInstall(var Name: String): String;
var
  ResultCode: Integer;
begin
  Result := '';
  Exec('taskkill', '/IM SoGame.exe /F', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

// 卸载时询问是否删除用户配置和密钥
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ConfigDir: string;
  KeyDir: string;
begin
  if CurUninstallStep = usUninstall then
  begin
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
