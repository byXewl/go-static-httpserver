@echo off
chcp 65001 >nul
echo ========================================
echo 静态目录服务器 - 简单构建脚本
echo ========================================
echo.
echo [信息] 使用 go build 构建（开发模式）...
echo.

go build -tags dev -o static-server.exe main.go

if %errorlevel% equ 0 (
    echo.
    echo ========================================
    echo 构建成功！
    echo ========================================
    echo 可执行文件: static-server.exe
    echo.
    echo 注意：这是开发模式构建，某些功能可能受限
    echo 建议使用 wails build 进行完整构建
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
