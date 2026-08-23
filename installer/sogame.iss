#define MyAppName "SoGame"
#define MyAppVersion "2.0"
#define MyAppPublisher "vir_dominary"
#define MyAppExeName "SoGame.exe"
#define NetBirdMSI "netbird_installer_0.74.7_windows_amd64.msi"

; ============================================================================
; 极速模式（netbird）打包说明
; ----------------------------------------------------------------------------
; 1. 主程序运行时按 exe 所在目录查找 sogame-helper.exe 与官方
; NetBird MSI（文件名见 internal/releasebuild/netbird-release.json 的
; windowsX64.artifact），找到后通过 UAC 提权（sogame-helper --action install
; --artifact <msi>）将其安装为 Windows 系统服务。
; 2. MSI（38MB 二进制产物）不随 git 仓库携带，但【必须】打进安装包，
;    否则新机器上极速模式会报“NetBird 服务未安装”且修复按钮无效。
;    编译前请确认 ..\bin\ 下存在官方 MSI；缺失时先运行
;    scripts\build-all.ps1（自动下载并校验 SHA256：
;    1be9ce80767a728a8682bc3c114256b224b8d6657400ac031e458a05b5e5942d）。
;    下方 NetBirdMSI 的 Source 行必须保持启用状态。
; ============================================================================

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
; 经典模式：n2n edge（应用按 exe 目录 / bin 子目录查找）
Source: "..\bin\edge.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
; 经典模式：TAP 网卡驱动（应用运行时从 {app}\installer\tap 加载并提权安装）
Source: "tap\OemWin2k.inf"; DestDir: "{app}\installer\tap"; Flags: ignoreversion
Source: "tap\tap0901.cat"; DestDir: "{app}\installer\tap"; Flags: ignoreversion
Source: "tap\tap0901.sys"; DestDir: "{app}\installer\tap"; Flags: ignoreversion
Source: "tap\tapinstall.exe"; DestDir: "{app}\installer\tap"; Flags: ignoreversion
; 极速模式：sogame-helper 提权助手（应用运行时与 exe 同级目录查找并 UAC 调用）
Source: "..\build\bin\sogame-helper.exe"; DestDir: "{app}"; Flags: ignoreversion
; 极速模式：官方 NetBird MSI（应用运行时与 exe 同级目录查找）。
; 缺失该文件时极速模式报“NetBird 服务未安装”且无法修复，禁止注释本行。
Source: "..\bin\{#NetBirdMSI}"; DestDir: "{app}"; Flags: ignoreversion

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式(&D)"; GroupDescription: "附加任务:"

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"

[Code]
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  ConfigDir: string;
  KeyDir: string;
begin
  if CurUninstallStep = usUninstall then
  begin
    if MsgBox('是否同时删除用户配置文件和本地密钥数据？', mbConfirmation, MB_YESNO) = IDYES then
    begin
      ConfigDir := ExpandConstant('{userappdata}\SoGame');
      if DirExists(ConfigDir) then
        DelTree(ConfigDir, True, True, True);
      KeyDir := ExpandConstant('{localappdata}\SoGame');
      if DirExists(KeyDir) then
        DelTree(KeyDir, True, True, True);
      Exec('netsh', 'advfirewall firewall delete rule name="SoGame VPN"',
           '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    end;
  end;
end;
