@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

:: UU远程进程名和路径配置
set "PROCESS_NAME=GameViewer.exe"
set "UU_PATH=E:\Program Files\Netease\GameViewer\GameViewer.exe"



:: 检查进程是否存在
tasklist /FI "IMAGENAME eq %PROCESS_NAME%" 2>nul | find /I "%PROCESS_NAME%" >nul

if %errorlevel%==0 (
    echo [%date% %time%] UU远程正在运行
) else (
    echo [%date% %time%] UU远程未运行，正在启动...
    if exist "%UU_PATH%" (
        start "" "%UU_PATH%"
        echo [%date% %time%] UU远程已启动
    ) else (
        echo [%date% %time%] 错误：找不到UU远程程序文件
    )
)