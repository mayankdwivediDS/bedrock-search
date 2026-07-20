@echo off
REM ──────────────────────────────────────────────────────────────────────
REM  build.bat — build go-suggest-neo binaries.
REM
REM  Usage:
REM      build.bat                    build for current OS (windows)
REM      build.bat linux              cross-compile linux/amd64 binaries
REM      build.bat windows            build windows/amd64 binaries
REM      build.bat all                build both windows and linux
REM      build.bat --help             show this help
REM
REM  Outputs:
REM      bin\neo-server.exe      bin\neo-bootstrap.exe        (windows)
REM      bin\linux\neo-server    bin\linux\neo-bootstrap      (linux)
REM ──────────────────────────────────────────────────────────────────────

setlocal ENABLEEXTENSIONS ENABLEDELAYEDEXPANSION
cd /d "%~dp0"

set "TARGET=%~1"
if "%TARGET%"=="" set "TARGET=windows"

if /I "%TARGET%"=="--help" goto help
if /I "%TARGET%"=="-h"     goto help

if not exist bin mkdir bin

echo [INFO] running go mod tidy...
go mod tidy
if errorlevel 1 (
    echo [FAIL] go mod tidy failed.
    exit /b 1
)

if /I "%TARGET%"=="windows" goto build_windows
if /I "%TARGET%"=="linux"   goto build_linux
if /I "%TARGET%"=="all"     goto build_all

echo Unknown target: %TARGET%
goto help

:build_all
call :do_windows || exit /b 1
call :do_linux   || exit /b 1
goto done

:build_windows
call :do_windows || exit /b 1
goto done

:build_linux
call :do_linux || exit /b 1
goto done

:do_windows
echo [INFO] building windows/amd64...
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -o "bin\neo-server.exe" .\cmd\server
if errorlevel 1 ( echo [FAIL] windows server build failed. & exit /b 1 )
go build -o "bin\neo-bootstrap.exe" .\cmd\bootstrap
if errorlevel 1 ( echo [FAIL] windows bootstrap build failed. & exit /b 1 )
echo [ OK ] windows build complete: bin\neo-server.exe, bin\neo-bootstrap.exe
exit /b 0

:do_linux
echo [INFO] building linux/amd64...
if not exist bin\linux mkdir bin\linux
set "GOOS=linux"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -o "bin\linux\neo-server" .\cmd\server
if errorlevel 1 ( echo [FAIL] linux server build failed. & exit /b 1 )
go build -o "bin\linux\neo-bootstrap" .\cmd\bootstrap
if errorlevel 1 ( echo [FAIL] linux bootstrap build failed. & exit /b 1 )
echo [ OK ] linux build complete: bin\linux\neo-server, bin\linux\neo-bootstrap
exit /b 0

:done
echo.
echo [DONE] build finished.
exit /b 0

:help
echo.
echo build.bat [windows^|linux^|all]
echo.
echo   windows   build windows/amd64 binaries (default)
echo   linux     cross-compile linux/amd64 binaries
echo   all       build both windows and linux
echo.
echo Outputs:
echo   bin\neo-server.exe, bin\neo-bootstrap.exe          (windows)
echo   bin\linux\neo-server, bin\linux\neo-bootstrap      (linux)
echo.
exit /b 0
