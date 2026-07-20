@echo off
REM ──────────────────────────────────────────────────────────────────────
REM  start_server.bat — build (optional) + run go-suggest-neo on Windows.
REM
REM  Usage:
REM      start_server.bat                       rebuild only if binary is missing
REM      start_server.bat --build true          force rebuild, then run
REM      start_server.bat --build false         skip build, run existing binary
REM      start_server.bat --help                show this help
REM
REM  The server picks up configuration from .env (if present) or OS env
REM  vars. See .env.example for the full list.
REM ──────────────────────────────────────────────────────────────────────

setlocal ENABLEEXTENSIONS
cd /d "%~dp0"

REM ── Parse --build <true|false> ──────────────────────────────────────
set "BUILD_FLAG=auto"
:parse
if "%~1"=="" goto args_done
if /I "%~1"=="--help"  goto help
if /I "%~1"=="-h"      goto help
if /I "%~1"=="--build" (
    if /I "%~2"=="true"  set "BUILD_FLAG=yes"
    if /I "%~2"=="false" set "BUILD_FLAG=no"
    if /I "%~2"=="1"     set "BUILD_FLAG=yes"
    if /I "%~2"=="0"     set "BUILD_FLAG=no"
    shift
    shift
    goto parse
)
echo Unknown argument: %~1
goto help
:args_done

REM ── Decide whether to build ─────────────────────────────────────────
set "SERVER_BIN=bin\neo-server.exe"
set "BOOT_BIN=bin\neo-bootstrap.exe"

if "%BUILD_FLAG%"=="auto" (
    if not exist "%SERVER_BIN%" (
        set "BUILD_FLAG=yes"
        echo [INFO] %SERVER_BIN% not found; will build.
    ) else (
        set "BUILD_FLAG=no"
    )
)

if "%BUILD_FLAG%"=="yes" (
    echo [INFO] building go-suggest-neo...
    if not exist bin mkdir bin
    go mod tidy
    if errorlevel 1 (
        echo [FAIL] go mod tidy failed.
        exit /b 1
    )
    go build -o "%SERVER_BIN%" .\cmd\server
    if errorlevel 1 (
        echo [FAIL] server build failed.
        exit /b 1
    )
    go build -o "%BOOT_BIN%" .\cmd\bootstrap
    if errorlevel 1 (
        echo [FAIL] bootstrap build failed.
        exit /b 1
    )
    echo [ OK ] build complete: %SERVER_BIN%
)

REM ── Sanity check before launch ──────────────────────────────────────
if not exist "%SERVER_BIN%" (
    echo [FAIL] %SERVER_BIN% does not exist. Run with --build true.
    exit /b 1
)
if not exist logs mkdir logs

REM ── Warn if no corpus has been bootstrapped yet ─────────────────────
if not defined DATA_DIR set "DATA_DIR=data"
if not exist "%DATA_DIR%\default\current.version" (
    echo.
    echo [WARN] no current.version at %DATA_DIR%\default\
    echo         Bootstrap a corpus first:
    echo             %BOOT_BIN% -source ^<your.json^> -data %DATA_DIR% -list default -version v1
    echo         Then re-run this script.
    echo.
    exit /b 1
)

REM ── Run ─────────────────────────────────────────────────────────────
echo [INFO] starting server...
"%SERVER_BIN%"
exit /b %ERRORLEVEL%

:help
echo.
echo start_server.bat [--build true^|false]
echo.
echo   --build true     force rebuild before launch
echo   --build false    skip build, run existing binary
echo   (no flag)        rebuild only if bin\neo-server.exe is missing
echo.
echo Config via environment variables or .env (see .env.example).
echo.
exit /b 0
