@echo off
chcp 65001 >nul
title Railway Revamped
cls

echo ===============================================================================
echo                           🚆 RAILWAY REVAMPED
echo ===============================================================================
echo.
echo [*] Setting up environment...
set "GOPATH=D:\Installs\Global Cache\go"
set "GOCACHE=D:\Installs\Gloabl Cache\go-build"
set "PORT=8080"

echo [*] Checking Go toolchain...
where go >nul 2>&1
if %errorlevel% neq 0 (
    echo [!] ERROR: Go compiler not found in system PATH.
    pause
    exit /b 1
)

echo [*] Application URL: http://localhost:%PORT%/
echo.
echo [*] Opening browser...
start "" cmd /c "timeout /t 2 /nobreak >nul & start http://localhost:%PORT%/"

echo [*] Starting server on port %PORT%...
cd /d "%~dp0backend\core"
go run ./cmd/server

if %errorlevel% neq 0 (
    echo.
    echo [!] Server stopped unexpectedly with exit code %errorlevel%.
    pause
)
