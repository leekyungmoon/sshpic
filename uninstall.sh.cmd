@echo off
setlocal EnableExtensions DisableDelayedExpansion

rem See install.sh.cmd: the missing exact .sh file is intentional so PowerShell
rem keeps literal ./uninstall.sh synchronous in the current terminal pane.
set "SSHPIC_GIT_SH=%ProgramFiles%\Git\bin\sh.exe"
if exist "%SSHPIC_GIT_SH%" goto run_uninstaller

set "SSHPIC_GIT_SH=%LocalAppData%\Programs\Git\bin\sh.exe"
if exist "%SSHPIC_GIT_SH%" goto run_uninstaller

set "SSHPIC_GIT_SH=%ProgramFiles(x86)%\Git\bin\sh.exe"
if exist "%SSHPIC_GIT_SH%" goto run_uninstaller

for /f "delims=" %%G in ('where.exe git.exe 2^>nul') do (
  if exist "%%~dpG..\bin\sh.exe" (
    set "SSHPIC_GIT_SH=%%~dpG..\bin\sh.exe"
    goto run_uninstaller
  )
)

echo sshpic uninstall failed: Git for Windows sh.exe was not found. 1>&2
echo Install Git for Windows, then rerun ./uninstall.sh from PowerShell. 1>&2
exit /b 69

:run_uninstaller
pushd "%~dp0" >nul || exit /b 1
"%SSHPIC_GIT_SH%" "./uninstall.sh.posix" %*
set "SSHPIC_STATUS=%ERRORLEVEL%"
popd >nul
exit /b %SSHPIC_STATUS%
