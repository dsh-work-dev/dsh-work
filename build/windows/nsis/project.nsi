Unicode true

; The Wails macro file is generated immediately before packaging and owns the
; WebView2 and architecture setup. dsh-work owns dsh:// and dsh-work:// below.
!define DSH_WORK_DISPLAY_NAME "DSH Work"
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
!define DSH_WORK_RETURN_PROTOCOL_KEY "Software\Classes\dsh-work"
!define DSH_WORK_PROTOCOL_BACKUP_KEY "${DSH_WORK_INSTALL_REG_KEY}\ProtocolBackup"

Var DshWorkInstallerMutex
Var DshWorkMutexName
Var DshWorkUserSID
Var DshWorkStagingDir
Var DshWorkBackupDir
Var DshWorkHadOld
Var DshWorkLogPath
Var DshWorkFailureStage
Var DshWorkUninstallStopped

Function dshwork.log
    Exch $0
    Push $1
    DetailPrint "$0"
    StrCmp $DshWorkLogPath "" dshworkLogDone
    ClearErrors
    FileOpen $1 "$DshWorkLogPath" a
    IfErrors dshworkLogDone
    FileSeek $1 0 END
    FileWrite $1 "$0$\r$\n"
    FileClose $1
dshworkLogDone:
    ClearErrors
    Pop $1
    Pop $0
FunctionEnd

Function dshwork.initLog
    ExpandEnvStrings $DshWorkLogPath "%USERPROFILE%\dsh-work-installer-debug.log"
    ClearErrors
    FileOpen $0 "$DshWorkLogPath" w
    IfErrors dshworkLogFileFailed
    FileWrite $0 "${DSH_WORK_DISPLAY_NAME} installer diagnostic log$\r$\n"
    FileWrite $0 "Log path: $DshWorkLogPath$\r$\n"
    FileClose $0
    ClearErrors
    Return
dshworkLogFileFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    DetailPrint "Could not create installer log file: $DshWorkLogPath (Win32 error $0)"
    StrCpy $DshWorkLogPath ""
    ClearErrors
FunctionEnd

; Preserve existing protocol handlers once, then keep both registrations owned
; by this install through upgrades. The unique return scheme remains reliable
; even if another app later claims dsh://.
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
    ClearErrors
    ReadRegDWORD $R0 SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "ReturnCaptured"
    StrCmp $R0 "1" dshworkRegisterProtocolValues
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}" "" "ReturnDescription"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}" "URL Protocol" "ReturnURLProtocol"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\DefaultIcon" "" "ReturnDefaultIcon"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell" "" "ReturnShellDefault"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open" "" "ReturnOpenDefault"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" "" "ReturnOpenCommand"
    !insertmacro dshwork.backupProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" "DelegateExecute" "ReturnDelegateExecute"
    WriteRegDWORD SHELL_CONTEXT "${DSH_WORK_PROTOCOL_BACKUP_KEY}" "ReturnCaptured" 1
dshworkRegisterProtocolValues:
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}" "" "URL: DeepSeek Harness Desktop Protocol"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}" "URL Protocol" ""
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\DefaultIcon" "" "$INSTDIR\${PRODUCT_EXECUTABLE},0"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell" "" "open"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell\open" "" ""
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" "" "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_RETURN_PROTOCOL_KEY}" "" "URL: ${DSH_WORK_DISPLAY_NAME} Desktop Return Link"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_RETURN_PROTOCOL_KEY}" "URL Protocol" ""
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_RETURN_PROTOCOL_KEY}\DefaultIcon" "" "$INSTDIR\${PRODUCT_EXECUTABLE},0"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell" "" "open"
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open" "" ""
    WriteRegStr SHELL_CONTEXT "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" "" "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
FunctionEnd

Function un.dshwork.restoreProtocol
    ReadRegStr $R0 SHELL_CONTEXT "${DSH_WORK_PROTOCOL_KEY}\shell\open\command" ""
    StrCpy $R1 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
    StrCmp $R0 $R1 unDshWorkRestoreProtocolOwned unDshWorkRestoreReturnProtocol
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
unDshWorkRestoreReturnProtocol:
    ReadRegStr $R0 SHELL_CONTEXT "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" ""
    StrCpy $R1 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
    StrCmp $R0 $R1 unDshWorkRestoreReturnProtocolOwned unDshWorkRestoreProtocolDone
unDshWorkRestoreReturnProtocolOwned:
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}" "URL Protocol"
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\DefaultIcon" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" ""
    !insertmacro dshwork.deleteProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" "DelegateExecute"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}" "" "ReturnDescription"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}" "URL Protocol" "ReturnURLProtocol"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\DefaultIcon" "" "ReturnDefaultIcon"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell" "" "ReturnShellDefault"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open" "" "ReturnOpenDefault"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" "" "ReturnOpenCommand"
    !insertmacro dshwork.restoreProtocolValue "${DSH_WORK_RETURN_PROTOCOL_KEY}\shell\open\command" "DelegateExecute" "ReturnDelegateExecute"
unDshWorkRestoreProtocolDone:
FunctionEnd

; Windows needs four numeric components for the PE version resource, and only
; numeric values are accepted there. INFO_FILE_VERSION is that mapping
; (X.Y.Z.0); INFO_RELEASE_VERSION is the full release version, which carries a
; -beta.N or -nightly.YYYYMMDD.<run id> pre-release part on pre-release builds.
!ifndef INFO_FILE_VERSION
    !define INFO_FILE_VERSION "${INFO_PRODUCTVERSION}.0"
!endif
!ifndef INFO_RELEASE_VERSION
    !define INFO_RELEASE_VERSION "${INFO_PRODUCTVERSION}"
!endif
VIProductVersion "${INFO_FILE_VERSION}"
VIFileVersion    "${INFO_FILE_VERSION}"
VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${DSH_WORK_DISPLAY_NAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_RELEASE_VERSION}"
VIAddVersionKey "FileVersion"     "${INFO_RELEASE_VERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${DSH_WORK_DISPLAY_NAME}"

ManifestDPIAware true
!include "MUI.nsh"
!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Open ${DSH_WORK_DISPLAY_NAME} after installation"
!define MUI_FINISHPAGE_RUN_FUNCTION dshwork.launch
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_COMPONENTS
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Name "${DSH_WORK_DISPLAY_NAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${INFO_RELEASE_VERSION}-windows-${ARCH}-installer.exe"
!if "${WAILS_INSTALL_SCOPE}" != "user"
    !error "${DSH_WORK_DISPLAY_NAME} requires a per-user installer"
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
    DetailPrint "Maintenance CLI staging directory: $PLUGINSDIR\dsh-work-stop"
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
${prefix}SIDFailed:
    ; No Return here: the macro is expanded inside the caller, and a Return
    ; would leave the caller before it creates the installer mutex.
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
    DetailPrint "Existing installation detected at: $INSTDIR"
    ; Stage the new CLI before replacing the old install. This keeps upgrades
    ; safe when the previous CLI predates the --wait coordination flag.
    IfSilent stopExistingContinue
    MessageBox MB_YESNO|MB_ICONQUESTION "${DSH_WORK_DISPLAY_NAME} will stop its background and any active task before maintenance. Continue?" IDYES stopExistingContinue
    Abort
stopExistingContinue:
    DetailPrint "Stopping the existing ${DSH_WORK_DISPLAY_NAME} background"
    !insertmacro dshwork.stageCliForStop
    ExecWait '"$PLUGINSDIR\dsh-work-stop\${CLI_EXECUTABLE}" stop --wait' $0
    DetailPrint "Maintenance CLI exit code: $0"
    ${If} $0 != 0
        MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not stop its background process. Installation was cancelled."
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
    MessageBox MB_OK|MB_ICONSTOP "Another ${DSH_WORK_DISPLAY_NAME} installer is already running."
    Abort
mutexFailed:
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not reserve its maintenance boundary."
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
    MessageBox MB_OK|MB_ICONSTOP "Another ${DSH_WORK_DISPLAY_NAME} installer is already running."
    Abort
unMutexFailed:
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not reserve its maintenance boundary."
    Abort
FunctionEnd

Function dshwork.recoverPreviousInstall
    ; A process crash after moving the old directory leaves this persistent
    ; recovery copy. A complete current install means the directory swap
    ; succeeded and only cleanup was interrupted; an incomplete current
    ; install is discarded before restoring the known-good copy.
    Push "Checking recovery directory: ${DSH_WORK_RECOVERY_DIR}"
    Call dshwork.log
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
    Push "Restoring previous installation from ${DSH_WORK_RECOVERY_DIR} to $INSTDIR"
    Call dshwork.log
    CreateDirectory "$LOCALAPPDATA\Programs"
    ClearErrors
    Rename "${DSH_WORK_RECOVERY_DIR}" "$INSTDIR"
    IfErrors recoveryFailed
    Push "Previous installation restored successfully."
    Call dshwork.log
    Return
recoveryCleanupFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "Could not remove completed recovery directory. Win32 error $0; path=${DSH_WORK_RECOVERY_DIR}"
    Call dshwork.log
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} finished a previous update but could not remove its recovery copy. Close ${DSH_WORK_DISPLAY_NAME} and run the installer again."
    Abort
partialCleanupFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "Could not remove incomplete installation. Win32 error $0; path=$INSTDIR"
    Call dshwork.log
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} found an incomplete installation but could not remove it. Close ${DSH_WORK_DISPLAY_NAME} and run the installer again."
    Abort
recoveryFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "Could not restore previous installation. Win32 error $0; source=${DSH_WORK_RECOVERY_DIR}; target=$INSTDIR"
    Call dshwork.log
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not restore the previous installation. Close ${DSH_WORK_DISPLAY_NAME} and run the installer again."
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
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not remove its recovery copy. Close ${DSH_WORK_DISPLAY_NAME} and run the uninstaller again."
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
    Push "Install replacement paths: temp=$PLUGINSDIR; staging=$DshWorkStagingDir; target=$INSTDIR; recovery=$DshWorkBackupDir"
    Call dshwork.log
    IfFileExists "$DshWorkBackupDir\*" backupConflict
    IfFileExists "$INSTDIR\*" 0 noOldInstall
    CreateDirectory "$LOCALAPPDATA\dsh-work"
    ClearErrors
    Rename "$INSTDIR" "$DshWorkBackupDir"
    IfErrors oldInstallMoveFailed
    StrCpy $DshWorkHadOld "1"
    Push "Moved existing installation to recovery directory: $DshWorkBackupDir"
    Call dshwork.log
noOldInstall:
    CreateDirectory "$LOCALAPPDATA\Programs"
    ClearErrors
    Rename "$DshWorkStagingDir" "$INSTDIR"
    IfErrors newInstallMoveFailed
    Push "Moved staged installation into target directory: $INSTDIR"
    Call dshwork.log
    ${If} $DshWorkHadOld == "1"
        ClearErrors
        RMDir /r "$DshWorkBackupDir"
        IfErrors recoveryCleanupFailed
    ${EndIf}
    Goto replaceInstallDone
oldInstallMoveFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "Could not move existing installation. Win32 error $0; source=$INSTDIR; target=$DshWorkBackupDir"
    Call dshwork.log
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not move the existing installation. Installation was cancelled."
    Abort
backupConflict:
    Push "Recovery directory already contains files: $DshWorkBackupDir"
    Call dshwork.log
    MessageBox MB_OK|MB_ICONSTOP "A previous ${DSH_WORK_DISPLAY_NAME} installation needs repair. The existing installation and recovery copy were kept."
    Abort
newInstallMoveFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "Could not move staged installation. Win32 error $0; source=$DshWorkStagingDir; target=$INSTDIR"
    Call dshwork.log
    ${If} $DshWorkHadOld == "1"
        Rename "$DshWorkBackupDir" "$INSTDIR"
        IfErrors rollbackFailed
    ${EndIf}
    Goto replaceFailed
rollbackFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "Installation rollback failed. Win32 error $0; source=$DshWorkBackupDir; target=$INSTDIR"
    Call dshwork.log
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not restore the previous installation. Use repair or reinstall from the original package."
    Abort
replaceFailed:
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not complete the installation. The previous installation was kept."
    Abort
recoveryCleanupFailed:
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} was installed but could not remove its recovery copy. Run the installer again to finish repair."
    Abort
replaceInstallDone:
!macroend

Function .onInit
    Call dshwork.initLog
    ExpandEnvStrings $0 "%TEMP%"
    Push "Environment TEMP: $0"
    Call dshwork.log
    ExpandEnvStrings $0 "%TMP%"
    Push "Environment TMP: $0"
    Call dshwork.log
    ExpandEnvStrings $0 "%LOCALAPPDATA%"
    Push "Environment LOCALAPPDATA: $0"
    Call dshwork.log
    InitPluginsDir
    IfErrors initPluginsDirFailed
    Push "NSIS plugin temp directory: $PLUGINSDIR"
    Call dshwork.log
    Goto initPluginsDirLogged
initPluginsDirFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "InitPluginsDir reported an error. Win32 error $0; TEMP=$TEMP; PLUGINSDIR=$PLUGINSDIR"
    Call dshwork.log
initPluginsDirLogged:
    ClearErrors
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
    Push "Resolved initial install directory: $INSTDIR"
    Call dshwork.log
FunctionEnd

Function dshwork.launch
    ; The finish callback runs before NSIS exits. Release the maintenance
    ; boundary first so the GUI can reconnect to its daemon immediately.
    Call dshwork.releaseInstallerMutex
    ExecShell "open" "$INSTDIR\${PRODUCT_EXECUTABLE}"
FunctionEnd

Function dshwork.launchAfterSilentInstall
    ; A silent install is an in-app update: finish it the same way, but start
    ; the workbench with Exec so it inherits the environment the update came
    ; from instead of the desktop shell's.
    Call dshwork.releaseInstallerMutex
    Exec '"$INSTDIR\${PRODUCT_EXECUTABLE}"'
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

Function un.dshwork.stopExistingOnce
    StrCmp $DshWorkUninstallStopped "1" unDshWorkStopExistingOnceDone
    !insertmacro dshwork.stopExisting
    StrCpy $DshWorkUninstallStopped "1"
unDshWorkStopExistingOnceDone:
FunctionEnd

Section "Program files" SEC_PROGRAM_FILES
    SectionIn RO
    !insertmacro wails.setShellContext
    Push "Installer section started. Selected install directory: $INSTDIR"
    Call dshwork.log
    !insertmacro dshwork.stopExisting
    Call dshwork.recoverPreviousInstall
    Push "Checking WebView2 runtime/bootstrapper. NSIS plugin temp directory: $PLUGINSDIR"
    Call dshwork.log
    !insertmacro wails.webview2runtime
    Push "WebView2 runtime/bootstrapper step completed."
    Call dshwork.log
    InitPluginsDir
    StrCpy $DshWorkStagingDir "$PLUGINSDIR\dsh-work-new"
    StrCpy $DshWorkFailureStage "create staging directory $DshWorkStagingDir"
    Push "Extracting application package to: $DshWorkStagingDir"
    Call dshwork.log
    ClearErrors
    SetOutPath "$DshWorkStagingDir"
    IfErrors packageStageFailed
    StrCpy $DshWorkFailureStage "extract GUI and CLI package into $DshWorkStagingDir"
    ClearErrors
    !insertmacro wails.files
    !insertmacro dshwork.cli
    IfErrors packageStageFailed
    StrCpy $DshWorkFailureStage "verify GUI executable $DshWorkStagingDir\${PRODUCT_EXECUTABLE}"
    IfFileExists "$DshWorkStagingDir\${PRODUCT_EXECUTABLE}" 0 packageStageFailed
    StrCpy $DshWorkFailureStage "verify CLI executable $DshWorkStagingDir\${CLI_EXECUTABLE}"
    IfFileExists "$DshWorkStagingDir\${CLI_EXECUTABLE}" 0 packageStageFailed
    StrCpy $DshWorkFailureStage "move staged install into $INSTDIR"
    !insertmacro dshwork.replaceInstall
    Call dshwork.registerProtocol
    WriteRegStr HKCU "${DSH_WORK_INSTALL_REG_KEY}" "Install_Dir" "$INSTDIR"

    CreateShortcut "$SMPROGRAMS\${DSH_WORK_DISPLAY_NAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    !insertmacro wails.associateFiles
    !insertmacro wails.writeUninstaller
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${DSH_WORK_DISPLAY_NAME}"
    WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${DSH_WORK_DISPLAY_NAME}"
    Goto installDone
packageStageFailed:
    System::Call 'kernel32::GetLastError() i.r0'
    Push "Application package failed during: $DshWorkFailureStage; Win32 last-error $0; TEMP=$TEMP; PLUGINSDIR=$PLUGINSDIR; staging=$DshWorkStagingDir; GUI=$DshWorkStagingDir\${PRODUCT_EXECUTABLE}; CLI=$DshWorkStagingDir\${CLI_EXECUTABLE}"
    Call dshwork.log
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not stage the complete application package. Installation was cancelled."
    Abort
installDone:
    Push "Installation completed successfully at: $INSTDIR"
    Call dshwork.log
    ; A silent install - the form an in-app update uses - never shows the finish
    ; page, so its launch callback would not run. Start the updated workbench
    ; here instead, after the maintenance boundary is released: starting it
    ; earlier would only make it report that maintenance is in progress.
    IfSilent 0 dshworkSectionEnd
    Call dshwork.launchAfterSilentInstall
dshworkSectionEnd:
SectionEnd

Section "Desktop shortcut" SEC_DESKTOP_SHORTCUT
    CreateShortcut "$DESKTOP\${DSH_WORK_DISPLAY_NAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
SectionEnd

Section /o "un.Delete user data" SEC_DELETE_USER_DATA
    !insertmacro wails.setShellContext
    Call un.dshwork.stopExistingOnce
    IfFileExists "$INSTDIR\${CLI_EXECUTABLE}" 0 uninstallPurgeCliMissing
    ClearErrors
    ExecWait '"$INSTDIR\${CLI_EXECUTABLE}" data purge --yes --during-uninstall' $0
    IfErrors uninstallPurgeStartFailed
    DetailPrint "User data purge exit code: $0"
    Goto uninstallPurgeDone
uninstallPurgeCliMissing:
    DetailPrint "User data purge could not run because the CLI executable is missing."
    Goto uninstallPurgeDone
uninstallPurgeStartFailed:
    DetailPrint "User data purge could not start."
uninstallPurgeDone:
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext
    Call un.dshwork.stopExistingOnce
    Call un.dshwork.cleanRecovery

    ; Application data is owned by the current user and remains available for
    ; a later reinstall. WebView2 is shared and is never removed here.
    ClearErrors
    RMDir /r "$INSTDIR"
    IfErrors uninstallInstallFailed
    Delete "$SMPROGRAMS\${DSH_WORK_DISPLAY_NAME}.lnk"
    Delete "$DESKTOP\${DSH_WORK_DISPLAY_NAME}.lnk"
    !insertmacro wails.unassociateFiles
    Call un.dshwork.restoreProtocol
    !insertmacro wails.deleteUninstaller
    SetRegView 64
    DeleteRegKey HKCU "${DSH_WORK_INSTALL_REG_KEY}"
    DeleteRegKey HKCU "Software\Classes\AppUserModelId\dsh-work"
    DeleteRegKey HKCU "Software\Classes\CLSID\{ca84fd06-433b-4c75-84ba-c74220f1f290}"
    Goto uninstallDone
uninstallInstallFailed:
    MessageBox MB_OK|MB_ICONSTOP "${DSH_WORK_DISPLAY_NAME} could not remove its installation. Close ${DSH_WORK_DISPLAY_NAME} and run the uninstaller again."
    Abort
uninstallDone:
SectionEnd
