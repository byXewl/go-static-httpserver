# 静态目录服务器

基于 Wails v2 框架的静态文件服务器 GUI 应用。

## 功能特性

- ✅ 图形化界面，操作简单
- ✅ 设置静态文件目录
- ✅ 选择监听网卡（自动获取本机所有IP地址）
- ✅ 设置监听端口（1-65535）
- ✅ 启动/停止服务器控制
- ✅ 实时日志显示
- ✅ 友好的错误提示

## 构建方法

### 方法1：使用 wails build（推荐）

1. 安装 Wails CLI：
   ```bash
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```

2. 运行构建：
   ```bash
   wails build
   ```

3. 可执行文件在 `build/bin` 目录中

### 方法2：使用 go build（开发模式）

```bash
go build -tags dev -o static-server.exe main.go
```

或者直接运行构建脚本：
```bash
build.bat
```

## 使用方法

1. 运行 `static-server.exe`
2. 在 GUI 界面中：
   - 选择或输入静态文件目录
   - 选择要监听的网卡 IP 地址
   - 输入端口号（默认 8085）
   - 点击"启动服务器"按钮
3. 访问服务器：
   - 静态文件：`http://IP:端口/`
   - API接口：`http://IP:端口/api/get`
   - JSON接口：`http://IP:端口/api/getjson`

## 系统要求

- Windows 10/11（需要 WebView2）
- Go 1.21.3 或更高版本

## 注意事项

- 确保选择的静态文件目录存在且可访问
- 端口范围必须在 1-65535 之间
- 如果端口被占用，程序会提示错误信息
