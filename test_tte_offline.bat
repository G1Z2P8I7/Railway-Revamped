@echo off
chcp 65001 >nul
title Railway Revamped — Offline TTE Validator
cls

echo ===============================================================================
echo        📱 RAILWAY REVAMPED — OFFLINE TTE HANDHELD QR VALIDATOR
echo ===============================================================================
echo [*] Testing Asymmetric Ed25519 Ticket Verification in Offline Airplane Mode
echo.

set "GOPATH=D:\Installs\Global Cache\go"
set "GOCACHE=D:\Installs\Gloabl Cache\go-build"

cd /d "%~dp0backend\core"
go run ./cmd/tte_validator -sample

echo.
pause
