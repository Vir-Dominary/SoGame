; SPDX-License-Identifier: AGPL-3.0-or-later
; Copyright (C) 2026 SoGame Contributors
;
; This file is part of SoGame.
;
; SoGame is free software: you can redistribute it and/or modify
; it under the terms of the GNU Affero General Public License as published by
; the Free Software Foundation, either version 3 of the License, or
; (at your option) any later version.
;
; SoGame is distributed in the hope that it will be useful,
; but WITHOUT ANY WARRANTY; without even the implied warranty of
; MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
; GNU Affero General Public License for more details.
;
; You should have received a copy of the GNU Affero General Public License
; along with SoGame. If not, see <https://www.gnu.org/licenses/>.

#define MyAppName "SoGame"
; 版本号单一事实源是 internal/config/app_config.go 的 AppVersion；
; build-installer.ps1 会解析该值并通过 ISCC /DMyAppVersion=<ver> 覆盖此默认值。
#ifndef MyAppVersion
  #define MyAppVersion "2.1"
#endif
#define MyAppPublisher "vir_dominary"
#define MyAppExeName "SoGame.exe"
; NetBird MSI 文件名单一事实源是 internal/releasebuild/netbird-release.json
; 的 windowsX64.artifact；build-installer.ps1 经 /DNetBirdMSI=<name> 注入，
; 此处默认值仅兜底手工 ISCC 编译。
#ifndef NetBirdMSI
  #define NetBirdMSI "netbird_installer_0.74.7_windows_amd64.msi"
#endif

; ============================================================================
; 极速模式（netbird）打包说明
; ----------------------------------------------------------------------------
; 1. 主程序运行时按 exe 所在目录查找 sogame-helper.exe 与官方
; NetBird MSI（文件名见 internal/releasebuild/netbird-release.json 的
; windowsX64.artifact），找到后通过 UAC 提权（sogame-helper --action install
; --artifact <msi>）将其安装为 Windows 系统服务。
; 2. MSI（38MB 二进制产物）不随 git 仓库携带，但【必须】打进安装包，
;    否则新机器上极速模式无法在 postinstall 预装（只能走运行时补齐路径）。
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
; 不允许 dialog 降级（PrivilegesRequiredOverridesAllowed=dialog 会让非管理员
; 完成安装——此时 helper asInvoker + msiexec 装系统服务必然失败，且 postinstall
; 的"安装器已提权"前提不成立，导致网络组件静默装不上）。
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
; 缺失该文件时极速模式需经"待准备"提示条在运行时补齐；
; 打进安装包可让新机器开箱即用（postinstall 预装），禁止注释本行。
Source: "..\bin\{#NetBirdMSI}"; DestDir: "{app}"; Flags: ignoreversion
; 许可证与版权信息（AGPLv3 正文、NOTICE、品牌政策、第三方许可证清单）
; 随安装包分发，确保二进制发行版用户可访问完整的许可证信息。
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\NOTICE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\TRADEMARK.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\THIRD_PARTY_LICENSES\*"; DestDir: "{app}\THIRD_PARTY_LICENSES"; Flags: ignoreversion recursesubdirs createallsubdirs

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式(&D)"; GroupDescription: "附加任务:"

[Run]
; 安装完成后静默安装 NetBird 网络组件（极速模式的内建依赖，
; 复用 sogame-helper 的 SHA256+签名+publisher+证书指纹四重校验链）。
; 安装器已提权（PrivilegesRequired=admin），此处不会再弹 UAC。
; 失败不阻断安装完成：首次使用极速模式时应用会自动补齐
; （运行时 EnsureDaemonReady 兜底，见 docs/DAEMON_AUTO_SETUP_DESIGN.md）；
; 失败时经 AfterInstall 读取 result 文件向用户提示（不再完全静默）。
Filename: "{app}\sogame-helper.exe"; Parameters: "--action install --artifact ""{app}\{#NetBirdMSI}"" --log ""{commonappdata}\SoGame\netbird-install.log"" --result ""{commonappdata}\SoGame\netbird-install.result"""; Flags: runhidden waituntilterminated; StatusMsg: "正在安装网络组件..."; AfterInstall: ReportNetBirdSetupResult

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"

[Code]
// ReportNetBirdSetupResult 在 postinstall 运行 sogame-helper 之后执行：
// 读取 helper 的 JSON result 文件，失败时向用户给出明确提示——此前
// helper 失败（校验不过/杀软拦截）安装照样"成功完成"，用户只能等到
// 首次使用极速模式时再吃一次 UAC，无任何指向性线索。
procedure ReportNetBirdSetupResult();
var
  ResultFile: string;
  Content: string;
begin
  ResultFile := ExpandConstant('{commonappdata}\SoGame\netbird-install.result');
  if not FileExists(ResultFile) then begin
    MsgBox('网络组件（极速模式依赖）未完成预装：未产生结果文件。' + #13#10 +
           '首次使用极速模式时会自动补齐，届时可能出现一次系统授权弹窗。',
           mbInformation, MB_OK);
    exit;
  end;
  if not LoadStringFromFile(ResultFile, Content) then begin
    MsgBox('网络组件（极速模式依赖）未完成预装：结果文件不可读。' + #13#10 +
           '首次使用极速模式时会自动补齐，届时可能出现一次系统授权弹窗。',
           mbInformation, MB_OK);
    exit;
  end;
  if Pos('"success":true', Content) = 0 then begin
    MsgBox('网络组件（极速模式依赖）未完成预装。' + #13#10 +
           '首次使用极速模式时会自动补齐；若持续失败，请检查安全软件设置。' + #13#10 +
           '详情见日志：' + ExpandConstant('{commonappdata}\SoGame\netbird-install.log'),
           mbInformation, MB_OK);
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
    // 防火墙规则属系统变更回滚，必须无条件删除——放在用户确认分支内
    // 会让选择"保留数据"的机器永久残留入站放行规则。
    // 系统程序必须用绝对路径（{sys} 展开 System32）：Inno 的 Exec 对
    // 裸名做 CreateProcess 完整搜索（含当前目录），卸载器运行于 TEMP
    // 随机目录，同用户进程可预置 netsh.exe 借管理员卸载提权执行。
    Exec(ExpandConstant('{sys}\netsh.exe'), 'advfirewall firewall delete rule name="SoGame VPN"',
         '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    if MsgBox('是否同时删除用户配置文件和本地密钥数据？', mbConfirmation, MB_YESNO) = IDYES then
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
