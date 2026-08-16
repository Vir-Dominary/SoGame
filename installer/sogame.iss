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
; 2. 本项目未随仓库携带 MSI（38MB 二进制产物）。编译安装包前请先下载：
;    https://github.com/netbirdio/netbird/releases/download/v0.74.7/netbird_installer_0.74.7_windows_amd64.msi
;    放到 ..\bin\ 目录（与下方 Source 路径一致），并校验 SHA256：
;    1be9ce80767a728a8682bc3c114256b224b8d6657400ac031e458a05b5e5942d
; 3. 未放置 MSI 时请保持下方 NetBirdMSI 行注释状态，安装包可正常编译，
;    仅极速模式首次安装能力缺失（TAP 驱动仍可正常使用）。
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
; 极速模式：官方 NetBird MSI（应用运行时与 exe 同级目录查找）。MSI 未随仓库
; 携带，放入 ..\bin\ 后取消本行注释再编译；保持注释则安装包不含极速模式安装能力。
; Source: "..\bin\{#NetBirdMSI}"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
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
