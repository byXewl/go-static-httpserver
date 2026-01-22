# 构建说明

Wails v2 需要使用 `wails build` 命令来构建应用程序。

## 方法1：使用 wails build（推荐）

1. 确保已安装 Wails CLI：
   ```bash
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```

2. 将 Wails 添加到 PATH（如果还没有）：
   - Windows: 将 `%USERPROFILE%\go\bin` 添加到系统 PATH
   - 或者直接使用完整路径：`%USERPROFILE%\go\bin\wails.exe build`

3. 运行构建命令：
   ```bash
   wails build
   ```

4. 构建完成后，可执行文件在 `build/bin` 目录中

## 方法2：使用 go build（需要构建标签）

如果无法使用 `wails build`，可以使用以下命令：

```bash
go build -tags dev -o static-server.exe main.go
```

注意：使用 `go build` 构建的版本可能缺少一些 Wails 特性。

## 快速构建脚本

运行 `build.bat` 文件即可自动构建。
