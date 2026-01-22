@echo off
chcp 65001 >nul
echo ========================================
echo 静态目录服务器 - 构建脚本
echo ========================================
echo.

REM 首先尝试使用 wails build
where wails >nul 2>&1
if %errorlevel% equ 0 (
    echo [信息] 使用 wails build 构建（推荐）...
    echo.
    wails build
    if %errorlevel% equ 0 (
        echo.
        echo ========================================
        echo 构建成功！
        echo ========================================
        echo 可执行文件位置: build\bin\static-server.exe
        echo.
        pause
        exit /b 0
    )
)

echo [信息] wails 命令不可用，使用 go build -tags dev 构建...
echo.

go build -tags dev -ldflags -H=windowsgui -o static-server.exe main.go

if %errorlevel% equ 0 (
    echo.
    echo ========================================
    echo 构建成功！
    echo ========================================
    echo 可执行文件: static-server.exe
    echo.
    echo 注意：这是开发模式构建
    echo 建议安装 wails CLI 并使用 wails build 进行完整构建
    echo.
) else (
    echo.
    echo ========================================
    echo 构建失败！
    echo ========================================
    echo 请检查错误信息
    echo.
    pause
    exit /b 1
)

pause
