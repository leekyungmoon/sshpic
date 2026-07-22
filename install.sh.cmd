@echo off
setlocal EnableExtensions DisableDelayedExpansion

rem PowerShell resolves literal ./install.sh to this .cmd through PATHEXT only
rem because no top-level file named exactly install.sh exists on this Windows branch.
rem Keep the POSIX implementation separate or Windows will use the .sh file
rem association and open Git Bash in another window before any repo code runs.
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
echo Install Git for Windows, then rerun ./install.sh from PowerShell. 1>&2
exit /b 69

:run_installer
pushd "%~dp0" >nul || exit /b 1
"%SSHPIC_GIT_SH%" "./install.sh.posix" %*
set "SSHPIC_STATUS=%ERRORLEVEL%"
popd >nul
exit /b %SSHPIC_STATUS%
