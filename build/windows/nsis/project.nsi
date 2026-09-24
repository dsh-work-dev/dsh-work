Unicode true

; The Wails macro file is generated immediately before packaging and owns the
; WebView2 and architecture setup. dsh-work owns scoped dsh:// registration below.
!include "wails_tools.nsh"

!ifndef CLI_EXECUTABLE
    !define CLI_EXECUTABLE "dsh-work-cli.exe"
!endif
!define TOKEN_QUERY 0x0008
!define TokenUser 1
!define DSH_WORK_INSTALLER_MUTEX_PREFIX "Global\dsh-work-installer-"
!define DSH_WORK_RECOVERY_DIR "$LOCALAPPDATA\dsh-work\installer-recovery"
!define DSH_WORK_INSTALL_REG_KEY "Software\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!define DSH_WORK_PROTOCOL_KEY "Software\Classes\dsh"
!define DSH_WORK_PROTOCOL_BACKUP_KEY "${DSH_WORK_INSTALL_REG_KEY}\ProtocolBackup"

Var DshWorkInstallerMutex
Var DshWorkMutexName
Var DshWorkUserSID
Var DshWorkStagingDir
Var DshWorkBackupDir
Var DshWorkHadOld

; Preserve the string values we replace in the user's existing dsh:// class.
; The values are captured once and survive upgrades until the final uninstall.
!macro dshwork.backupProtocolValue REGKEY VALUE NAME
    ClearErrors
    ReadRegStr $R0 SHELL_CONTEXT "${REGKEY}" "${VALUE}"
    IfErrors dshworkBackup${NAME}Missing
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "${NAME}" "$R0"
    WriteRegDWORD SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "${NAME}_Present" 1
    Goto dshworkBackup${NAME}Done
dshworkBackup${NAME}Missing:
    WriteRegDWORD SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "${NAME}_Present" 0
dshworkBackup${NAME}Done:
!macroend

!macro dshwork.restoreProtocolValue REGKEY VALUE NAME
    ClearErrors
    ReadRegDWORD $R0 SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "${NAME}_Present"
    StrCmp $R0 "1" dshworkRestore${NAME}Present dshworkRestore${NAME}Done
dshworkRestore${NAME}Present:
    ReadRegStr $R1 SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "${NAME}"
    WriteRegStr SHELL_CONTEXT "${REGKEY}" "${VALUE}" "$R1"
dshworkRestore${NAME}Done:
!macroend

!macro dshwork.deleteProtocolValue REGKEY VALUE
    DeleteRegValue SHELL_CONTEXT "${REGKEY}" "${VALUE}"
!macroend

Function dshwork.registerProtocol
    ClearErrors
    ReadRegDWORD $R0 SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "Captured"
    StrCmp $R0 "1" dshworkRegisterProtocolAssociation
    ReadRegStr $R0 HKCU "${DSH_WORK_INSTALL_REG_KEY}" "Install_Dir"
    ReadRegStr $R1 SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" ""
    StrCmp $R0 "" dshworkBackupExternalProtocol
    StrCpy $R2 "$\"$R0\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
    StrCmp $R1 $R2 dshworkBackupPreviousDshWorkProtocol dshworkBackupExternalProtocol
dshworkBackupPreviousDshWorkProtocol:
    ; The earlier dsh-work installer registered the same handler without a
    ; restorable backup. Do not preserve its path as an external application.
    WriteRegDWORD SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "Captured" 1
    Goto dshworkRegisterProtocolAssociation
dshworkBackupExternalProtocol:
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_PROTOCOL_KEY}" "" "Description"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_PROTOCOL_KEY}" "URL Protocol" "URLProtocol"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_PROTOCOL_KEY}\DefaultIcon" "" "DefaultIcon"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell" "" "ShellDefault"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open" "" "OpenDefault"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" "" "OpenCommand"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" "DelegateExecute" "DelegateExecute"
    WriteRegDWORD SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "Captured" 1
dshworkRegisterProtocolAssociation:
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}" "" "URL: DeepSeek Harness Desktop Protocol"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}" "URL Protocol" ""
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\DefaultIcon" "" "$INSTDIR\${PRODUCT_EXECUTABLE},0"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell" "" "open"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell\open" "" ""
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" "" "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
FunctionEnd

Function un.dshwork.restoreProtocol
    ReadRegStr $R0 SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" ""
    StrCpy $R1 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
    StrCmp $R0 $R1 unDshWorkRestoreProtocolOwned unDshWorkRestoreProtocolDone
unDshWorkRestoreProtocolOwned:
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_PROTOCOL_KEY}" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_PROTOCOL_KEY}" "URL Protocol"
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_PROTOCOL_KEY}\DefaultIcon" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" "DelegateExecute"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_PROTOCOL_KEY}" "" "Description"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_PROTOCOL_KEY}" "URL Protocol" "URLProtocol"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_PROTOCOL_KEY}\DefaultIcon" "" "DefaultIcon"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell" "" "ShellDefault"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open" "" "OpenDefault"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" "" "OpenCommand"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" "DelegateExecute" "DelegateExecute"
unDshWorkRestoreProtocolDone:
FunctionEnd

; Wails requires four numeric components for PE metadata. The first E-21
; release line uses the numeric three-component source version and appends 0.
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true
!include "MUI.nsh"
!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Open dsh-work after installation"
!define MUI_FINISHPAGE_RUN_FUNCTION dshwork.launch
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${INFO_PRODUCTVERSION}-windows-${ARCH}-installer.exe"
!if "${WAILS_INSTALL_SCOPE}" != "user"
    !error "dsh-work requires a per-user installer"
!endif
InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
ShowInstDetails show

; The CLI is a sibling executable in the same release unit. It keeps its
; console subsystem and is selected for the installer's target architecture.
!macro dshwork.cli
    !ifdef ARG_DSH_WORK_CLI_AMD64_BINARY
        !ifdef SUPPORTS_AMD64
            ${if} ${IsNativeAMD64}
                File "/oname=${CLI_EXECUTABLE}" "${ARG_DSH_WORK_CLI_AMD64_BINARY}"
            ${EndIf}
        !endif
    !endif
    !ifdef ARG_DSH_WORK_CLI_ARM64_BINARY
        !ifdef SUPPORTS_ARM64
            ${if} ${IsNativeARM64}
                File "/oname=${CLI_EXECUTABLE}" "${ARG_DSH_WORK_CLI_ARM64_BINARY}"
            ${EndIf}
        !endif
    !endif
!macroend

!macro dshwork.stageCliForStop
    InitPluginsDir
    SetOutPath "$PLUGINSDIR\dsh-work-stop"
    !insertmacro dshwork.cli
!macroend

!macro dshwork.resolveUserSID prefix
    ; Resolve the SID from the current process token. Environment variables
    ; such as USERNAME are mutable and cannot provide a cross-session user
    ; boundary.
    StrCpy $DshWorkUserSID ""
    System::Call 'kernel32::GetCurrentProcess()p.r0'
    System::Call 'advapi32::OpenProcessToken(p r0, i ${TOKEN_QUERY}, *p .r1)i.r2'
    StrCmp $2 0 ${prefix}SIDFailed
    System::Call 'advapi32::GetTokenInformation(p r1, i ${TokenUser}, p 0, i 0, *i .r3)i.r2'
    StrCmp $3 0 ${prefix}SIDClose
    System::Alloc $3
    Pop $4
    StrCmp $4 0 ${prefix}SIDClose
    System::Call 'advapi32::GetTokenInformation(p r1, i ${TokenUser}, p r4, i r3, *i .r3)i.r2'
    StrCmp $2 0 ${prefix}SIDFree
    System::Call '*$4(p.r5)'
    StrCmp $5 0 ${prefix}SIDFree
    System::Call 'advapi32::ConvertSidToStringSid(p r5, *t .r6)i.r2'
    StrCmp $2 0 ${prefix}SIDFree
    StrCpy $DshWorkUserSID $6
    System::Call 'kernel32::LocalFree(p r6)p.r7'
${prefix}SIDFree:
    System::Free $4
${prefix}SIDClose:
    System::Call 'kernel32::CloseHandle(p r1)'
    Return
${prefix}SIDFailed:
    Return
!macroend

!macro dshwork.stopExisting
    ; A clean first install has no executable to stop. Only stage the CLI and
    ; run the maintenance handshake when this user already has an installed
    ; release unit at the selected directory.
    IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" stopExistingRequired
    IfFileExists "$INSTDIR\${CLI_EXECUTABLE}" stopExistingRequired
    IfFileExists "$INSTDIR\uninstall.exe" stopExistingRequired
    Goto stopExistingDone
stopExistingRequired:
    ; Stage the new CLI before replacing the old install. This keeps upgrades
    ; safe when the previous CLI predates the --wait coordination flag.
    IfSilent stopExistingContinue
    MessageBox MB_YESNO|MB_ICONQUESTION "dsh-work will stop its background and any active task before maintenance. Continue?" IDYES stopExistingContinue
    Abort
stopExistingContinue:
    DetailPrint "Stopping the existing dsh-work background"
    !insertmacro dshwork.stageCliForStop
    ExecWait '"$PLUGINSDIR\dsh-work-stop\${CLI_EXECUTABLE}" stop --wait' $0
    ${If} $0 != 0
        MessageBox MB_OK|MB_ICONSTOP "dsh-work could not stop its background process. Installation was cancelled."
        Abort
    ${EndIf}
stopExistingDone:
!macroend

Function dshwork.acquireInstallerMutex
    ; The mutex spans the complete NSIS process. The daemon and UI probe the
    ; same current-user object before they can start or reconnect.
    !insertmacro dshwork.resolveUserSID dshWork
    StrCmp $DshWorkUserSID "" mutexFailed
    StrCpy $DshWorkMutexName "${DSH_WORK_INSTALLER_MUTEX_PREFIX}$DshWorkUserSID"
    System::Call 'kernel32::CreateMutex(p 0, i 0, t "$DshWorkMutexName") p .r1 ?e'
    Pop $0
    StrCpy $DshWorkInstallerMutex $1
    StrCmp $0 183 alreadyRunning
    StrCmp $1 0 mutexFailed
    Return
alreadyRunning:
    System::Call 'kernel32::CloseHandle(p $DshWorkInstallerMutex)'
    MessageBox MB_OK|MB_ICONSTOP "Another dsh-work installer is already running."
    Abort
mutexFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not reserve its maintenance boundary."
    Abort
FunctionEnd

Function un.dshwork.acquireInstallerMutex
    ; The uninstaller has its own callback namespace. Keep the same mutex
    ; boundary without calling the installer's function from un.onInit.
    !insertmacro dshwork.resolveUserSID unDshWork
    StrCmp $DshWorkUserSID "" unMutexFailed
    StrCpy $DshWorkMutexName "${DSH_WORK_INSTALLER_MUTEX_PREFIX}$DshWorkUserSID"
    System::Call 'kernel32::CreateMutex(p 0, i 0, t "$DshWorkMutexName") p .r1 ?e'
    Pop $0
    StrCpy $DshWorkInstallerMutex $1
    StrCmp $0 183 unAlreadyRunning
    StrCmp $1 0 unMutexFailed
    Return
unAlreadyRunning:
    System::Call 'kernel32::CloseHandle(p $DshWorkInstallerMutex)'
    MessageBox MB_OK|MB_ICONSTOP "Another dsh-work installer is already running."
    Abort
unMutexFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not reserve its maintenance boundary."
    Abort
FunctionEnd

Function dshwork.recoverPreviousInstall
    ; A process crash after moving the old directory leaves this persistent
    ; recovery copy. A complete current install means the directory swap
    ; succeeded and only cleanup was interrupted; an incomplete current
    ; install is discarded before restoring the known-good copy.
    IfFileExists "${DSH_WORK_RECOVERY_DIR}\*" 0 recoveryDone
    IfFileExists "$INSTDIR\*" 0 restoreRecovery
    IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 discardPartialInstall
    IfFileExists "$INSTDIR\${CLI_EXECUTABLE}" 0 discardPartialInstall
    RMDir /r "${DSH_WORK_RECOVERY_DIR}"
    IfErrors recoveryCleanupFailed
    Return
discardPartialInstall:
    ClearErrors
    RMDir /r "$INSTDIR"
    IfErrors partialCleanupFailed
    Goto restoreRecovery
restoreRecovery:
    CreateDirectory "$LOCALAPPDATA\Programs"
    ClearErrors
    Rename "${DSH_WORK_RECOVERY_DIR}" "$INSTDIR"
    IfErrors recoveryFailed
    Return
recoveryCleanupFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work finished a previous update but could not remove its recovery copy. Close dsh-work and run the installer again."
    Abort
partialCleanupFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work found an incomplete installation but could not remove it. Close dsh-work and run the installer again."
    Abort
recoveryFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not restore the previous installation. Close dsh-work and run the installer again."
    Abort
recoveryDone:
FunctionEnd

Function un.dshwork.cleanRecovery
    IfFileExists "${DSH_WORK_RECOVERY_DIR}\*" 0 unCleanRecoveryDone
    ClearErrors
    RMDir /r "${DSH_WORK_RECOVERY_DIR}"
    IfErrors unCleanRecoveryFailed
unCleanRecoveryDone:
    Return
unCleanRecoveryFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not remove its recovery copy. Close dsh-work and run the uninstaller again."
    Abort
FunctionEnd

!macro dshwork.replaceInstall
    ; Extract both executables into a temporary directory first. Moving the
    ; complete directory makes a failed second move restorable instead of
    ; leaving a GUI/CLI pair from different releases.
    ; SetOutPath changes the installer's current directory. Leave the staging
    ; tree before moving it, otherwise Windows can keep the directory busy.
    SetOutPath "$PLUGINSDIR"
    StrCpy $DshWorkStagingDir "$PLUGINSDIR\dsh-work-new"
    StrCpy $DshWorkBackupDir "${DSH_WORK_RECOVERY_DIR}"
    StrCpy $DshWorkHadOld "0"
    IfFileExists "$DshWorkBackupDir\*" backupConflict
    IfFileExists "$INSTDIR\*" 0 noOldInstall
    CreateDirectory "$LOCALAPPDATA\dsh-work"
    ClearErrors
    Rename "$INSTDIR" "$DshWorkBackupDir"
    IfErrors oldInstallMoveFailed
    StrCpy $DshWorkHadOld "1"
noOldInstall:
    CreateDirectory "$LOCALAPPDATA\Programs"
    ClearErrors
    Rename "$DshWorkStagingDir" "$INSTDIR"
    IfErrors newInstallMoveFailed
    ${If} $DshWorkHadOld == "1"
        ClearErrors
        RMDir /r "$DshWorkBackupDir"
        IfErrors recoveryCleanupFailed
    ${EndIf}
    Goto replaceInstallDone
oldInstallMoveFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not move the existing installation. Installation was cancelled."
    Abort
backupConflict:
    MessageBox MB_OK|MB_ICONSTOP "A previous dsh-work installation needs repair. The existing installation and recovery copy were kept."
    Abort
newInstallMoveFailed:
    ${If} $DshWorkHadOld == "1"
        Rename "$DshWorkBackupDir" "$INSTDIR"
        IfErrors rollbackFailed
    ${EndIf}
    Goto replaceFailed
rollbackFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not restore the previous installation. Use repair or reinstall from the original package."
    Abort
replaceFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not complete the installation. The previous installation was kept."
    Abort
recoveryCleanupFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work was installed but could not remove its recovery copy. Run the installer again to finish repair."
    Abort
replaceInstallDone:
!macroend

Function .onInit
    !insertmacro wails.checkArchitecture
    ; InstallDirRegKey is evaluated before .onInit and would use NSIS's
    ; default registry view. Read the user-owned path explicitly after
    ; selecting the 64-bit view used when the installer writes it.
    SetRegView 64
    ReadRegStr $0 HKCU "${DSH_WORK_INSTALL_REG_KEY}" "Install_Dir"
    ${If} $0 != ""
        StrCpy $INSTDIR $0
    ${EndIf}
    Call dshwork.acquireInstallerMutex
FunctionEnd

Function dshwork.launch
    ; The finish callback runs before NSIS exits. Release the maintenance
    ; boundary first so the GUI can reconnect to its daemon immediately.
    Call dshwork.releaseInstallerMutex
    ExecShell "open" "$INSTDIR\${PRODUCT_EXECUTABLE}"
FunctionEnd

Function dshwork.releaseInstallerMutex
    StrCmp $DshWorkInstallerMutex 0 releaseInstallerMutexDone
    System::Call 'kernel32::CloseHandle(p $DshWorkInstallerMutex)'
    StrCpy $DshWorkInstallerMutex 0
releaseInstallerMutexDone:
FunctionEnd

Function un.onInit
    ; NSIS may copy the uninstaller to a temporary directory before running
    ; it. Restore the real user-selected install directory before any cleanup.
    SetRegView 64
    ReadRegStr $0 HKCU "${DSH_WORK_INSTALL_REG_KEY}" "Install_Dir"
    ${If} $0 != ""
        StrCpy $INSTDIR $0
    ${EndIf}
    Call un.dshwork.acquireInstallerMutex
FunctionEnd

Section
    !insertmacro wails.setShellContext
    !insertmacro dshwork.stopExisting
    Call dshwork.recoverPreviousInstall
    !insertmacro wails.webview2runtime
    InitPluginsDir
    StrCpy $DshWorkStagingDir "$PLUGINSDIR\dsh-work-new"
    SetOutPath "$DshWorkStagingDir"
    !insertmacro wails.files
    !insertmacro dshwork.cli
    IfFileExists "$DshWorkStagingDir\${PRODUCT_EXECUTABLE}" 0 packageStageFailed
    IfFileExists "$DshWorkStagingDir\${CLI_EXECUTABLE}" 0 packageStageFailed
    !insertmacro dshwork.replaceInstall
    Call dshwork.registerProtocol
    WriteRegStr HKCU "${DSH_WORK_INSTALL_REG_KEY}" "Install_Dir" "$INSTDIR"

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    !insertmacro wails.associateFiles
    !insertmacro wails.writeUninstaller
    Goto installDone
packageStageFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not stage the complete application package. Installation was cancelled."
    Abort
installDone:
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext
    !insertmacro dshwork.stopExisting
    Call un.dshwork.cleanRecovery

    ; Application data is owned by the current user and remains available for
    ; a later reinstall. WebView2 is shared and is never removed here.
    ClearErrors
    RMDir /r "$INSTDIR"
    IfErrors uninstallInstallFailed
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    !insertmacro wails.unassociateFiles
    Call un.dshwork.restoreProtocol
    !insertmacro wails.deleteUninstaller
    SetRegView 64
    DeleteRegKey HKCU "${DSH_WORK_INSTALL_REG_KEY}"
    Goto uninstallDone
uninstallInstallFailed:
    MessageBox MB_OK|MB_ICONSTOP "dsh-work could not remove its installation. Close dsh-work and run the uninstaller again."
    Abort
uninstallDone:
SectionEnd
