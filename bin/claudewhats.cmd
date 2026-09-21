@echo off
setlocal enabledelayedexpansion

set "BIN=%CLAUDEWHATS_BIN%"
if "%BIN%"=="" set "BIN=%USERPROFILE%\.claudewhats\bin\claudewhats.exe"

if "%PROCESSOR_ARCHITECTURE%"=="ARM64" (
    set "ARCH=arm64"
) else (
    set "ARCH=amd64"
)

set "VERSION=%CLAUDEWHATS_VERSION%"
if "%VERSION%"=="" set "VERSION=latest"
if "%VERSION%"=="latest" (
    set "RELEASE_PATH=latest/download"
) else (
    set "RELEASE_PATH=download/%VERSION%"
)

set "ASSET=claudewhats_windows_%ARCH%.exe"
set "URL=https://github.com/andrewmautone/ClaudeWhats/releases/%RELEASE_PATH%/%ASSET%"

if not exist "%BIN%" (
    for %%D in ("%BIN%") do set "BINDIR=%%~dpD"
    if not exist "!BINDIR!" mkdir "!BINDIR!" >nul 2>&1
    echo baixando claudewhats %ASSET%... 1>&2
    curl.exe -fsSL -o "%BIN%.tmp" "%URL%"
    if errorlevel 1 (
        echo erro: falha ao baixar %URL% 1>&2
        exit /b 1
    )
    move /y "%BIN%.tmp" "%BIN%" >nul
)

"%BIN%" %*
exit /b %ERRORLEVEL%
