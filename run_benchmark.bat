@echo off
chcp 65001 >nul
title Railway Revamped — Concurrency Benchmark
cls

echo ===============================================================================
echo            🚆 RAILWAY REVAMPED — HIGH-THROUGHPUT CONCURRENCY AUDIT
echo ===============================================================================
echo [*] Testing 250,000 - 300,000 bookings/min Tatkal surge capacity
echo [*] Validating sub-300ms p99 latency and 0.00%% double-bookings
echo.

set "GOPATH=D:\Installs\Global Cache\go"
set "GOCACHE=D:\Installs\Gloabl Cache\go-build"

cd /d "%~dp0backend\core"
go run ./cmd/benchmark

echo.
pause
