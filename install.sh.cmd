@echo off
setlocal EnableExtensions DisableDelayedExpansion

rem cmd.exe fallback for the public ./install.sh command. The shell entrypoint
rem detects Windows and continues through the bundled PowerShell facade.
set "SSHPIC_GIT_SH=%ProgramFiles%\Git\bin\sh.exe"
if exist "%SSHPIC_GIT_SH%" goto run_installer

set "SSHPIC_GIT_SH=%LocalAppData%\Programs\Git\bin\sh.exe"
if exist "%SSHPIC_GIT_SH%" goto run_installer

set "SSHPIC_GIT_SH=%ProgramFiles(x86)%\Git\bin\sh.exe"
if exist "%SSHPIC_GIT_SH%" goto run_installer

for /f "delims=" %%G in ('where.exe git.exe 2^>nul') do (
  if exist "%%~dpG..\bin\sh.exe" (
    set "SSHPIC_GIT_SH=%%~dpG..\bin\sh.exe"
    goto run_installer
  )
)

echo sshpic installation failed: Git for Windows sh.exe was not found. 1>&2
echo Install Git for Windows, then rerun ./install.sh. 1>&2
exit /b 69

:run_installer
pushd "%~dp0" >nul || exit /b 1
set "SSHPIC_INSTALL_KEEP_POWERSHELL=1"
"%SSHPIC_GIT_SH%" "./install.sh" %*
set "SSHPIC_STATUS=%ERRORLEVEL%"
popd >nul
exit /b %SSHPIC_STATUS%
