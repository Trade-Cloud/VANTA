@echo off
rem VANTA - Windows build wrapper. Arguments are passed to build.ps1
rem Example: build.bat -Debugger -Clean
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" %*
set ERR=%ERRORLEVEL%
if not "%ERR%"=="0" (
  echo.
  echo Build failed with code %ERR%
  if "%~1"=="" pause
)
exit /b %ERR%
