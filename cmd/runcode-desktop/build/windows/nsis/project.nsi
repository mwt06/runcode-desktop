Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
## 
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "XRUN"
## !define INFO_COMPANYNAME    "My Company" # Default "My Company"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "My Product"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "0.1.0"
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "© now, My Company"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
## !define WAILS_INSTALL_SCOPE     "user"             # Default "machine" - set to "user" for per-user install ($LOCALAPPDATA) without UAC prompt
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps

# 完成页上的「运行」勾选框：装完直接能用，不必再去找图标。首次安装(用户手工双击
# 安装包)才看得到——应用自己触发的更新走 /S 静默，整个向导都不显示。
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "运行 ${INFO_PRODUCTNAME}"
!define MUI_FINISHPAGE_RUN_FUNCTION launchAsUser
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    ; 默认装到 C:\Program Files\Ouc\desk_agent。$PROGRAMFILES64 跟着系统盘走,不写死 C:;
    ; 目录名写死而不是拼「公司名\产品名」,是要让安装路径全 ASCII、不随品牌显示名变化。
    ; 只对**全新安装**生效:已装过的机器由 reuseExistingInstallDir 沿用上一次的目录。
    InstallDir "$PROGRAMFILES64\Ouc\desk_agent"
!endif
ShowInstDetails show # This will always show the installation details.

; 以**当前用户**的普通权限启动应用，不继承安装器的管理员权限。
;
; 安装器是提权跑的,直接 Exec 会让应用也带着管理员身份运行。那不只是"权限大了点":
; 它写进数据目录(%APPDATA%\runcode 等)的文件会变成 admin 属主,之后用户以普通权限
; 正常打开应用时,反而读不动自己上一次留下的会话和配置。
;
; explorer.exe 始终以登录用户的中完整性级别运行,借它转一手拉起来的进程也就回到中
; 完整性——这是不引入额外插件的标准做法。
; 等应用退出、目标 exe 不再被占用，然后才动它的文件。
;
; 更新是**应用自己拉起安装器**的：它先启动安装器,再退出自己(Windows 不允许覆盖正在
; 运行的可执行文件)。静默安装比可视向导快得多,会在应用还没退干净时就走到复制文件
; 那一步——而 NSIS 在静默模式下遇到写不动的文件是**跳过并继续**,于是留下最坏的一种
; 状态:注册表写成了新版本、exe 还是旧的、全程没有任何报错。这不是假想,实测复现过:
; 装完之后「添加或删除程序」显示 0.1.3,跑起来的还是 0.1.2,而且更新提示会一直在。
;
; 判据用「能不能以写方式打开那个 exe」,不是「进程在不在」:进程名可以被改、可以有
; 多个,而文件锁才是真正挡住复制的那个东西。
;
; 等满仍占用就**整个放弃**,不写任何东西。宁可这次没装上(用户重来一次即可),也不要
; 留下一个自称是新版的旧程序——后者会让人对着一个"更新过了却还是旧行为"的东西查半天。
Function waitForAppExit
    IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 done
    StrCpy $R9 0
  loop:
    ClearErrors
    FileOpen $R8 "$INSTDIR\${PRODUCT_EXECUTABLE}" a
    IfErrors busy
    FileClose $R8
    Goto done
  busy:
    IntCmp $R9 60 giveup 0 giveup   ; 60 × 500ms = 30 秒
    IntOp $R9 $R9 + 1
    Sleep 500
    Goto loop
  giveup:
    DetailPrint "「${INFO_PRODUCTNAME}」仍在运行,无法覆盖,已取消本次安装。"
    Abort "「${INFO_PRODUCTNAME}」仍在运行,请先完全退出再安装。"
  done:
FunctionEnd

Function launchAsUser
    Exec '"$WINDIR\explorer.exe" "$INSTDIR\${PRODUCT_EXECUTABLE}"'
FunctionEnd

Function .onInit
   !insertmacro wails.checkArchitecture
   Call reuseExistingInstallDir
FunctionEnd

; 升级时沿用上一次的安装目录。
;
; 不加这一段的后果是实打实出过的:用户把应用装在 D:\Program Files\...,点「检查更新」
; 装上新版之后,新版落在 InstallDir 那个硬编码的默认路径(C:\Program Files\...),
; D 盘那份原地不动 —— 机器上从此有两份,快捷方式指向哪一份全看运气,而单实例锁会让
; 后启动的那份直接静默退出。表现是"更新完好像没更新"。
;
; NSIS 本身不会记住装在哪;Wails 的模板也没写 InstallLocation(见 wails_tools.nsh 的
; wails.writeUninstaller),所以这里按两级去找:
;
;   1. InstallLocation —— 本安装器自己写的(见下面 Section 里补的那句),新版起才有;
;   2. UninstallString —— 0.1.3 及更早**已经**写了它,形如 "<目录>\uninstall.exe",
;      去掉引号取父目录就是旧的安装目录。有了这一级,从老版本升上来的用户第一次就
;      能落回原处,不必等到装过一次新版之后。
;
; 两个根键都查:machine 范围写 HKLM、user 范围写 HKCU,而用户上一次用的是哪种范围,
; 安装器这边并不知道。
Function reuseExistingInstallDir
    SetRegView 64

    ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
    ${If} $0 == ""
        ReadRegStr $0 HKCU "${UNINST_KEY}" "InstallLocation"
    ${EndIf}
    ${If} $0 != ""
    ${AndIf} ${FileExists} "$0\*.*"
        StrCpy $INSTDIR $0
        Return
    ${EndIf}

    ReadRegStr $1 HKLM "${UNINST_KEY}" "UninstallString"
    ${If} $1 == ""
        ReadRegStr $1 HKCU "${UNINST_KEY}" "UninstallString"
    ${EndIf}
    ${If} $1 == ""
        Return
    ${EndIf}
    ; 去掉两端的引号(写进去时是带引号的)
    StrCpy $2 $1 1
    ${If} $2 == '"'
        StrCpy $1 $1 "" 1
        StrCpy $1 $1 -1
    ${EndIf}
    ${GetParent} "$1" $3
    ${If} $3 != ""
    ${AndIf} ${FileExists} "$3\*.*"
        StrCpy $INSTDIR $3
    ${EndIf}
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR
    
    ; 必须在复制文件之前:见 waitForAppExit 的说明(半截更新就是这么来的)。
    Call waitForAppExit

    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols
    
    !insertmacro wails.writeUninstaller

    ; 记下这次装到哪儿了,下次升级好沿用(Wails 的模板不写这个值,见 reuseExistingInstallDir)。
    ; InstallLocation 也是「添加或删除程序」里那一栏的来源,顺带把它填对。
    SetRegView 64
    !if "${WAILS_INSTALL_SCOPE}" == "user"
        WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    !else
        WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    !endif
SectionEnd

Section "uninstall" 
    !insertmacro wails.setShellContext

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
