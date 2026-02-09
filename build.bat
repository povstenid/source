@echo off
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -ldflags "-s -w" -o pnat .
if %ERRORLEVEL% == 0 (
    echo Build OK: pnat [linux/amd64]
) else (
    echo Build FAILED
)
