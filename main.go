package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx       context.Context
	server    *http.Server
	mu        sync.Mutex
	isRunning bool
	saveLogs  bool
	logs      []string
	logMu     sync.Mutex
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// IPInfo IP地址信息
type IPInfo struct {
	IP   string `json:"ip"`
	Name string `json:"name"`
}

// GetLocalIPs 获取本机所有IP地址
func (a *App) GetLocalIPs() []IPInfo {
	var ips []IPInfo
	ips = append(ips, IPInfo{IP: "127.0.0.1", Name: "本地"}, IPInfo{IP: "0.0.0.0", Name: "所有网卡"})

	interfaces, err := net.Interfaces()
	if err != nil {
		return ips
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip != nil && ip.To4() != nil {
				ipStr := ip.String()
				found := false
				for _, existingIP := range ips {
					if existingIP.IP == ipStr {
						found = true
						break
					}
				}
				if !found {
					// 提取网卡名称作为来源标识
					name := iface.Name
					// 完整显示网卡名称，不进行截断
					ips = append(ips, IPInfo{IP: ipStr, Name: name})
				}
			}
		}
	}

	return ips
}

// handleFileRequest handles file serving, uploads, and folder creation
func (a *App) handleFileRequest(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Handle POST requests
		if r.Method == "POST" {
			// Check if it's a folder creation or file creation request
			contentType := r.Header.Get("Content-Type")
			if strings.Contains(contentType, "application/json") {
				a.handleJSONRequest(w, r, root)
				return
			}
			// Otherwise handle file upload
			a.handleUpload(w, r, root)
			return
		}

		// Handle File Serving / Directory Listing
		fullPath := filepath.Join(root, strings.TrimPrefix(path, "/"))

		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		if info.IsDir() {
			// Check for index.html
			indexFile := filepath.Join(fullPath, "index.html")
			if _, err := os.Stat(indexFile); err == nil {
				http.ServeFile(w, r, indexFile)
				return
			}

			// Serve directory listing
			a.serveDirectory(w, r, fullPath, path)
		} else {
			http.ServeFile(w, r, fullPath)
		}
	}
}

// handleUpload handles file uploads
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request, root string) {
	// Parse multipart form, max 32MB
	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	uploadDir := filepath.Join(root, strings.TrimPrefix(r.URL.Path, "/"))
	// Ensure uploadDir exists and is a directory
	info, err := os.Stat(uploadDir)
	if err != nil || !info.IsDir() {
		http.Error(w, "Invalid upload directory", http.StatusBadRequest)
		return
	}

	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			a.addLog(fmt.Sprintf("Error opening uploaded file: %v", err))
			continue
		}

		// Prevent directory traversal in filename
		filename := filepath.Base(fileHeader.Filename)
		dstPath := filepath.Join(uploadDir, filename)

		dst, err := os.Create(dstPath)
		if err != nil {
			file.Close()
			a.addLog(fmt.Sprintf("Error creating file %s: %v", dstPath, err))
			continue
		}

		if _, err := io.Copy(dst, file); err != nil {
			a.addLog(fmt.Sprintf("Error saving file %s: %v", dstPath, err))
		} else {
			a.addLog(fmt.Sprintf("File uploaded: %s", dstPath))
		}

		dst.Close()
		file.Close()
	}

	w.WriteHeader(http.StatusOK)
}

// handleFolderCreation handles folder creation requests
func (a *App) handleFolderCreation(w http.ResponseWriter, r *http.Request, root string) {
	// Parse JSON request
	var req struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check if it's a folder creation request
	if req.Action != "createFolder" {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	// Validate folder name
	if req.Name == "" {
		http.Error(w, "Folder name is required", http.StatusBadRequest)
		return
	}

	// Prevent directory traversal
	if strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") || strings.Contains(req.Name, "..") {
		http.Error(w, "Invalid folder name", http.StatusBadRequest)
		return
	}

	// Get target directory
	targetDir := filepath.Join(root, strings.TrimPrefix(r.URL.Path, "/"))

	// Ensure target directory exists and is a directory
	info, err := os.Stat(targetDir)
	if err != nil || !info.IsDir() {
		http.Error(w, "Invalid target directory", http.StatusBadRequest)
		return
	}

	// Create full path for new folder
	newFolderPath := filepath.Join(targetDir, req.Name)

	// Check if folder already exists
	if _, err := os.Stat(newFolderPath); err == nil {
		http.Error(w, "Folder already exists", http.StatusConflict)
		return
	}

	// Create the folder
	if err := os.MkdirAll(newFolderPath, 0755); err != nil {
		http.Error(w, "Failed to create folder: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Log the folder creation
	a.addLog(fmt.Sprintf("Folder created: %s", newFolderPath))

	// Return success
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Folder created successfully"))
}

// handleFileCreation handles file creation requests
func (a *App) handleFileCreation(w http.ResponseWriter, r *http.Request, root string) {
	// Parse JSON request
	var req struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check if it's a file creation request
	if req.Action != "createFile" {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	// Validate file name
	if req.Name == "" {
		http.Error(w, "File name is required", http.StatusBadRequest)
		return
	}

	// Prevent directory traversal
	if strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") || strings.Contains(req.Name, "..") {
		http.Error(w, "Invalid file name", http.StatusBadRequest)
		return
	}

	// Get target directory
	targetDir := filepath.Join(root, strings.TrimPrefix(r.URL.Path, "/"))

	// Ensure target directory exists and is a directory
	info, err := os.Stat(targetDir)
	if err != nil || !info.IsDir() {
		http.Error(w, "Invalid target directory", http.StatusBadRequest)
		return
	}

	// Create full path for new file
	newFilePath := filepath.Join(targetDir, req.Name)

	// Check if file already exists
	if _, err := os.Stat(newFilePath); err == nil {
		http.Error(w, "File already exists", http.StatusConflict)
		return
	}

	// Create the file
	if _, err := os.Create(newFilePath); err != nil {
		http.Error(w, "Failed to create file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Log the file creation
	a.addLog(fmt.Sprintf("File created: %s", newFilePath))

	// Return success
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("File created successfully"))
}

// handleJSONRequest handles JSON requests and routes to the appropriate handler
func (a *App) handleJSONRequest(w http.ResponseWriter, r *http.Request, root string) {
	// Parse JSON request to get the action
	var req struct {
		Action string `json:"action"`
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Reset the request body so it can be read again
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	// Parse the action
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Reset the request body again for the actual handler
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	// Route to the appropriate handler based on action
	if req.Action == "createFolder" {
		a.handleFolderCreation(w, r, root)
	} else if req.Action == "createFile" {
		a.handleFileCreation(w, r, root)
	} else {
		http.Error(w, "Invalid action", http.StatusBadRequest)
	}
}

// serveDirectory serves a custom directory listing with upload button
func (a *App) serveDirectory(w http.ResponseWriter, r *http.Request, dirPath, requestPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Sort entries: directories first, then files
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() && !entries[j].IsDir() {
			return true
		}
		if !entries[i].IsDir() && entries[j].IsDir() {
			return false
		}
		return entries[i].Name() < entries[j].Name()
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Generate HTML
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Index of %s</title>
	<style>
			body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; padding: 20px; line-height: 1.5; background-color: #fff; color: #333; }
			h1 { margin-bottom: 20px; border-bottom: 1px solid #eaeaea; padding-bottom: 10px; font-size: 24px; }
			ul { list-style: none; padding: 0; }
			li { padding: 10px 0; border-bottom: 1px solid #f0f0f0; display: flex; align-items: center; }
			li:hover { background-color: #f8f9fa; }
			a { text-decoration: none; color: #007bff; flex-grow: 1; display: block; margin-left: 10px; }
			a:hover { text-decoration: underline; }
			.icon { width: 24px; text-align: center; display: inline-block; font-size: 1.2em; }
			.size { color: #666; font-size: 0.9em; margin-left: 20px; min-width: 80px; text-align: right; }
			.fab {
				position: fixed;
				bottom: 30px;
				right: 30px;
				width: 60px;
				height: 60px;
				border-radius: 50%%;
				background-color: #007bff;
				color: white;
				font-size: 30px;
				border: none;
				box-shadow: 0 4px 12px rgba(0,0,0,0.3);
				cursor: pointer;
				display: flex;
				align-items: center;
				justify-content: center;
				transition: background-color 0.3s, transform 0.2s;
				z-index: 1000;
			}
			.fab:hover { background-color: #0056b3; transform: scale(1.05); }
			.fab:active { transform: scale(0.95); }
			
			/* Modal styles */
			.modal {
				display: none;
				position: fixed;
				z-index: 2000;
				left: 0;
				top: 0;
				width: 100%;
				height: 100%;
				overflow: auto;
				background-color: rgba(0,0,0,0.4);
			}
			.modal-content {
				background-color: rgba(255, 255, 255, 0.95);
				position: absolute;
				right: 30px;
				top: 30px;
				padding: 20px;
				border: 1px solid rgba(136, 136, 136, 0.5);
				width: 300px;
				border-radius: 8px;
				box-shadow: 0 4px 12px rgba(0,0,0,0.2);
				backdrop-filter: blur(5px);
			}
			.modal-content h3 {
				margin-top: 0;
				color: #333;
			}
			.modal-content input {
				width: 100%;
				padding: 10px;
				margin: 10px 0 20px 0;
				border: 1px solid #ddd;
				border-radius: 4px;
				box-sizing: border-box;
			}
			.modal-buttons {
				display: flex;
				gap: 10px;
				justify-content: flex-end;
			}
			.modal-buttons button {
				padding: 8px 16px;
				border: none;
				border-radius: 4px;
				cursor: pointer;
				font-size: 14px;
			}
			.modal-buttons .btn-create {
				background-color: #28a745;
				color: white;
			}
			.modal-buttons .btn-create:hover {
				background-color: #218838;
			}
			.modal-buttons .btn-cancel {
				background-color: #6c757d;
				color: white;
			}
			.modal-buttons .btn-cancel:hover {
				background-color: #5a6268;
			}
		</style>
</head>
<body>
	<h1>Index of %s</h1>
	<ul>`, requestPath, requestPath)

	// Parent directory link
	if requestPath != "/" {
		parent := filepath.Dir(strings.TrimSuffix(requestPath, "/"))
		// Fix Windows path issues when filepath.Dir returns \
		parent = strings.ReplaceAll(parent, "\\", "/")
		if !strings.HasPrefix(parent, "/") {
			parent = "/" + parent
		}
		fmt.Fprintf(w, `<li><span class="icon">📁</span><a href="%s">..</a></li>`, parent)
	}

	for _, entry := range entries {
		name := entry.Name()
		isDir := entry.IsDir()
		icon := "📄"
		if isDir {
			icon = "📁"
		}

		// Encode URL
		urlPath := url.PathEscape(name)
		if isDir {
			urlPath += "/"
		}

		// Get size
		sizeStr := "-"
		if !isDir {
			info, err := entry.Info()
			if err == nil {
				sizeStr = formatSize(info.Size())
			}
		}

		displayName := name
		if isDir {
			displayName += "/"
		}

		fmt.Fprintf(w, `<li><span class="icon">%s</span><a href="%s">%s</a><span class="size">%s</span></li>`,
			icon, urlPath, displayName, sizeStr)
	}

	fmt.Fprintf(w, `</ul>

	<button class="fab" onclick="toggleActions()" title="Actions">+</button>
	<input type="file" id="file-upload" style="display: none" multiple onchange="uploadFiles(this.files)">

	<!-- Action Menu -->
	<div id="action-menu" style="display: none; position: fixed; bottom: 100px; right: 30px; background: white; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.3); z-index: 999; padding: 10px; min-width: 150px;">
		<button onclick="event.stopPropagation(); document.getElementById('file-upload').click(); document.getElementById('action-menu').style.display = 'none';" style="width: 100%; padding: 10px; text-align: left; border: none; background: none; cursor: pointer; border-radius: 4px; margin: 2px 0;">
			📤 上传文件
		</button>
		<button onclick="event.stopPropagation(); createFolder(); document.getElementById('action-menu').style.display = 'none';" style="width: 100%; padding: 10px; text-align: left; border: none; background: none; cursor: pointer; border-radius: 4px; margin: 2px 0;">
			📁 创建文件夹
		</button>
		<button onclick="event.stopPropagation(); createFile(); document.getElementById('action-menu').style.display = 'none';" style="width: 100%; padding: 10px; text-align: left; border: none; background: none; cursor: pointer; border-radius: 4px; margin: 2px 0;">
			📄 创建文件
		</button>
	</div>

	<script>
		// Toggle action menu
		function toggleActions() {
			const menu = document.getElementById('action-menu');
			menu.style.display = menu.style.display === 'none' ? 'block' : 'none';
		}

		// Close menu when clicking outside
		document.addEventListener('click', function(event) {
			const menu = document.getElementById('action-menu');
			const fab = document.querySelector('.fab');
			if (!menu.contains(event.target) && !fab.contains(event.target)) {
				menu.style.display = 'none';
			}
		});

		// Folder creation function
		function createFolder() {
			const folderName = prompt('请输入文件夹名称:');
			if (!folderName || folderName.trim() === '') {
				return; // 用户取消或输入为空
			}

			const trimmedName = folderName.trim();

			// Validate folder name
			if (trimmedName.includes('/') || trimmedName.includes('\\') || trimmedName.includes('..')) {
				alert('无效的文件夹名称');
				return;
			}

			// Send request to create folder
			fetch(window.location.href, {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({ action: 'createFolder', name: trimmedName })
			}).then(response => {
				if (response.ok) {
					location.reload();
				} else {
					return response.text().then(text => {
						alert('创建文件夹失败: ' + text);
					});
				}
			}).catch(err => {
				alert('错误: ' + err);
			});
		}

		// File creation function
		function createFile() {
			const fileName = prompt('请输入文件名称:');
			if (!fileName || fileName.trim() === '') {
				return; // 用户取消或输入为空
			}

			const trimmedName = fileName.trim();

			// Validate file name
			if (trimmedName.includes('/') || trimmedName.includes('\\') || trimmedName.includes('..')) {
				alert('无效的文件名称');
				return;
			}

			// Send request to create file
			fetch(window.location.href, {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({ action: 'createFile', name: trimmedName })
			}).then(response => {
				if (response.ok) {
					location.reload();
				} else {
					return response.text().then(text => {
						alert('创建文件失败: ' + text);
					});
				}
			}).catch(err => {
				alert('错误: ' + err);
			});
		}

		// Upload files function
		function uploadFiles(files) {
			if (!files.length) return;

			const formData = new FormData();
			for (let i = 0; i < files.length; i++) {
				formData.append('files', files[i]);
			}

			const btn = document.querySelector('.fab');
			const originalText = btn.innerText;
			btn.innerText = '...';
			btn.disabled = true;

			fetch(window.location.href, {
				method: 'POST',
				body: formData
			}).then(response => {
				if (response.ok) {
					location.reload();
				} else {
					alert('Upload failed: ' + response.statusText);
				}
			}).catch(err => {
				alert('Error: ' + err);
			}).finally(() => {
				btn.innerText = originalText;
				btn.disabled = false;
				document.getElementById('file-upload').value = '';
			});
		}
	</script>
</body>
</html>`)
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// StartServer 启动服务器
func (a *App) StartServer(dir, ip, port string) map[string]interface{} {
	result := make(map[string]interface{})

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isRunning {
		result["success"] = false
		result["message"] = "服务器已经在运行中！"
		return result
	}

	// 验证目录
	if dir == "" {
		result["success"] = false
		result["message"] = "请选择静态文件目录！"
		return result
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		result["success"] = false
		result["message"] = fmt.Sprintf("无法获取目录绝对路径: %v", err)
		return result
	}

	info, err := os.Stat(absDir)
	if err != nil {
		result["success"] = false
		result["message"] = fmt.Sprintf("目录不存在或无法访问: %v", err)
		return result
	}

	if !info.IsDir() {
		result["success"] = false
		result["message"] = "选择的路径不是目录！"
		return result
	}

	// 验证IP地址
	if ip == "" {
		result["success"] = false
		result["message"] = "请选择或输入监听IP地址！"
		return result
	}

	if net.ParseIP(ip) == nil {
		result["success"] = false
		result["message"] = fmt.Sprintf("无效的IP地址: %s", ip)
		return result
	}

	// 验证端口
	if port == "" {
		result["success"] = false
		result["message"] = "请输入监听端口！"
		return result
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		result["success"] = false
		result["message"] = fmt.Sprintf("端口必须是数字: %v", err)
		return result
	}

	if portNum < 1 || portNum > 65535 {
		result["success"] = false
		result["message"] = "端口范围必须在 1-65535 之间！"
		return result
	}

	// 检查端口是否被占用
	addr := fmt.Sprintf("%s:%d", ip, portNum)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		result["success"] = false
		result["message"] = fmt.Sprintf("端口 %d 已被占用或无法监听: %v\n\n请尝试更换端口或检查防火墙设置。", portNum, err)
		return result
	}
	listener.Close()

	// 创建新的HTTP服务器
	mux := http.NewServeMux()

	// 动态路由：/api/get/{id} -> 读取 api/txt/{id}.txt
	mux.HandleFunc("/api/get/", func(w http.ResponseWriter, r *http.Request) {
		// 获取ID
		id := strings.TrimPrefix(r.URL.Path, "/api/get/")
		if id == "" {
			http.NotFound(w, r)
			return
		}

		// 安全检查：防止目录遍历
		if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		// 构建文件路径：api/txt/{id}.txt
		filePath := filepath.Join("api", "txt", id+".txt")

		// 读取文件内容
		content, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
			} else {
				// 记录错误日志但不暴露给客户端详细路径
				log.Printf("Error reading file %s: %v", filePath, err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
			return
		}

		// 设置响应头为 JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write(content)
	})

	// 动态路由：/api/getjson/{id} -> 读取 api/json/{id}.json
	mux.HandleFunc("/api/getjson/", func(w http.ResponseWriter, r *http.Request) {
		// 获取ID
		id := strings.TrimPrefix(r.URL.Path, "/api/getjson/")
		if id == "" {
			http.NotFound(w, r)
			return
		}

		// 安全检查
		if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		// 构建文件路径：api/json/{id}.json
		filePath := filepath.Join("api", "json", id+".json")

		// 读取文件内容
		content, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
			} else {
				log.Printf("Error reading file %s: %v", filePath, err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
			return
		}

		// 设置响应头为 JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write(content)
	})

	// 静态文件服务器 (使用自定义处理器支持上传)
	mux.HandleFunc("/", a.handleFileRequest(absDir))

	a.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// 在goroutine中启动服务器
	go func() {
		a.addLog("正在启动服务器...")
		a.addLog(fmt.Sprintf("静态目录: %s", absDir))
		a.addLog(fmt.Sprintf("监听地址: %s", addr))
		a.addLog(fmt.Sprintf("访问地址: http://%s/", addr))
		a.addLog(fmt.Sprintf("API接口(示例): http://%s/api/get/2  ==> 响应./api/txt/2.txt", addr))
		a.addLog(fmt.Sprintf("API接口(json示例): http://%s/api/getjson/1 ==> 响应./api/json/1.json", addr))
		a.addLog("----------------------------------------")

		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.mu.Lock()
			a.isRunning = false
			a.mu.Unlock()

			a.addLog(fmt.Sprintf("服务器错误: %v", err))
		}
	}()

	a.isRunning = true
	result["success"] = true
	result["message"] = fmt.Sprintf("服务器启动成功！\n访问地址: http://%s/", addr)
	a.addLog("服务器启动成功！")
	return result
}

// StopServer 停止服务器
func (a *App) StopServer() map[string]interface{} {
	result := make(map[string]interface{})

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.isRunning {
		result["success"] = false
		result["message"] = "服务器未运行！"
		return result
	}

	if a.server != nil {
		if err := a.server.Close(); err != nil {
			a.addLog(fmt.Sprintf("停止服务器时出错: %v", err))
			result["success"] = false
			result["message"] = fmt.Sprintf("停止服务器时出错: %v", err)
			return result
		} else {
			a.addLog("服务器已停止")
		}
	}

	a.isRunning = false
	result["success"] = true
	result["message"] = "服务器已停止"
	return result
}

// GetLogs 获取日志
func (a *App) GetLogs() []string {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	logs := make([]string, len(a.logs))
	copy(logs, a.logs)
	return logs
}

// ClearLogs 清空日志
func (a *App) ClearLogs() {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.logs = []string{}
}

// SelectDirectory 选择目录
func (a *App) SelectDirectory() string {
	// 使用 Wails 的目录选择对话框
	dialog, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择静态文件目录",
	})
	if err != nil {
		return ""
	}
	return dialog
}

// addLog 添加日志
func (a *App) addLog(message string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.logs = append(a.logs, message)
	// 限制日志数量
	if len(a.logs) > 100 {
		a.logs = a.logs[len(a.logs)-100:]
	}

	// 如果开启了日志保存，写入文件
	if a.saveLogs {
		logDir := "log"
		if err := os.MkdirAll(logDir, 0755); err == nil {
			timestamp := time.Now().Format("2006-01-02 15:04:05")
			logMsg := fmt.Sprintf("[%s] %s\n", timestamp, message)

			f, err := os.OpenFile(filepath.Join(logDir, "log.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				defer f.Close()
				f.WriteString(logMsg)
			}
		}
	}
}

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title: "静态目录服务器 - byXe",
		// 也可以设置为其他标题，例如：
		// Title:  "My Static Server",
		Width:  700,
		Height: 600,
		AssetServer: &assetserver.Options{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" || r.URL.Path == "/index.html" {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Write([]byte(getHTML()))
				} else if r.URL.Path == "/api/getLocalIPs" {
					w.Header().Set("Content-Type", "application/json")
					ips := app.GetLocalIPs()
					json.NewEncoder(w).Encode(ips)
				} else if r.URL.Path == "/api/startServer" && r.Method == "POST" {
					var req struct {
						Dir  string `json:"dir"`
						IP   string `json:"ip"`
						Port string `json:"port"`
					}
					json.NewDecoder(r.Body).Decode(&req)
					result := app.StartServer(req.Dir, req.IP, req.Port)
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(result)
				} else if r.URL.Path == "/api/stopServer" && r.Method == "POST" {
					result := app.StopServer()
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(result)
				} else if r.URL.Path == "/api/getLogs" {
					logs := app.GetLogs()
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(logs)
				} else if r.URL.Path == "/api/clearLogs" && r.Method == "POST" {
					app.ClearLogs()
					w.WriteHeader(http.StatusOK)
				} else if r.URL.Path == "/api/toggleSaveLogs" && r.Method == "POST" {
					var req struct {
						Enable bool `json:"enable"`
					}
					json.NewDecoder(r.Body).Decode(&req)
					app.logMu.Lock()
					app.saveLogs = req.Enable
					app.logMu.Unlock()
					if req.Enable {
						app.addLog("日志保存已开启")
					} else {
						app.addLog("日志保存已关闭")
					}
					w.WriteHeader(http.StatusOK)
				} else if r.URL.Path == "/api/selectDirectory" {
					result := app.SelectDirectory()
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(result)
				} else if r.URL.Path == "/api/qrcode" {
					// 生成二维码
					data := r.URL.Query().Get("data")
					if data == "" {
						http.Error(w, "Missing data parameter", http.StatusBadRequest)
						return
					}
					// 设置响应头为图片
					w.Header().Set("Content-Type", "image/png")
					// 使用 go-qrcode 库生成二维码并直接写入响应
					err := qrcode.WriteFile(data, qrcode.Medium, 200, "./temp_qr.png")
					if err != nil {
						http.Error(w, "Failed to generate QR code", http.StatusInternalServerError)
						return
					}
					// 读取生成的二维码图片并发送
					imgData, err := os.ReadFile("./temp_qr.png")
					if err != nil {
						http.Error(w, "Failed to read QR code image", http.StatusInternalServerError)
						return
					}
					// 发送图片数据
					w.Write(imgData)
					// 删除临时文件
					os.Remove("./temp_qr.png")
				} else {
					http.NotFound(w, r)
				}
			}),
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
	})

	if err != nil {
		log.Fatal(err)
	}
}

// HTML界面
func getHTML() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>静态目录服务器 - byXe</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: 'Microsoft YaHei', Arial, sans-serif;
            padding: 20px;
            background: #f5f5f5;
        }
        .container {
            max-width: 800px;
            margin: 0 auto;
            background: white;
            padding: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
        }
        /* 隐藏滚动条但保留滚动功能 */
        ::-webkit-scrollbar {
            display: none;
        }
        * {
            -ms-overflow-style: none;
            scrollbar-width: none;
        }
        h1 {
            text-align: center;
            color: #333;
            margin-bottom: 20px;
        }
        .form-group {
            margin-bottom: 15px;
        }
        label {
            display: block;
            margin-bottom: 5px;
            color: #555;
            font-weight: bold;
        }
        input, select {
            width: 100%;
            padding: 8px;
            border: 1px solid #ddd;
            border-radius: 4px;
            font-size: 14px;
        }
        .input-group {
            display: flex;
            gap: 10px;
        }
        .input-group input {
            flex: 1;
        }
        .input-group button {
            padding: 8px 15px;
            background: #007bff;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
        }
        .input-group button:hover {
            background: #0056b3;
        }
        .button-group {
            display: flex;
            gap: 10px;
            margin-top: 20px;
        }
        button {
            padding: 10px 20px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
            flex: 1;
        }
        .btn-start {
            background: #28a745;
            color: white;
        }
        .btn-start:hover {
            background: #218838;
        }
        .btn-stop {
            background: #dc3545;
            color: white;
        }
        .btn-stop:hover {
            background: #c82333;
        }
        .btn-clear {
            background: #6c757d;
            color: white;
        }
        .btn-clear:hover {
            background: #5a6268;
        }
        .btn-stop:disabled {
            background: #ccc;
            cursor: not-allowed;
        }
        .log-area {
            margin-top: 20px;
            padding: 10px;
            background: #f8f9fa;
            border: 1px solid #ddd;
            border-radius: 4px;
            height: 200px;
            overflow-y: auto;
            font-family: 'Consolas', monospace;
            font-size: 12px;
        }
        .log-line {
            padding: 2px 0;
            color: #333;
        }
        .message {
            margin-top: 10px;
            padding: 10px;
            border-radius: 4px;
            display: none;
        }
        .message.success {
            background: #d4edda;
            color: #155724;
            border: 1px solid #c3e6cb;
        }
        .message.error {
            background: #f8d7da;
            color: #721c24;
            border: 1px solid #f5c6cb;
        }
        .message.show {
            display: block;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>静态目录服务器配置</h1>
        
        <div class="form-group">
            <label>静态目录:</label>
            <div class="input-group">
                <input type="text" id="dir" placeholder="选择或输入静态文件目录" value="./">
                <button onclick="browseDirectory()">浏览...</button>
            </div>
        </div>

        <div class="form-group">
            <label>监听网卡:</label>
            <select id="ip">
                <option value="127.0.0.1">127.0.0.1 (本地)</option>
                <option value="0.0.0.0">0.0.0.0 (所有网卡)</option>
            </select>
        </div>

        <div class="form-group">
            <label>监听端口:</label>
            <input type="text" id="port" placeholder="1-65535" value="8085">
        </div>

        <div class="button-group">
            <button class="btn-start" onclick="start()">启动服务器</button>
            <button class="btn-stop" id="stopBtn" onclick="stop()" disabled>停止服务器</button>
            <button class="btn-clear" onclick="clearLog()">清空日志</button>
        </div>
        <div style="margin-top: 10px; display: flex; align-items: center;">
            <input type="checkbox" id="saveLogs" onchange="toggleSaveLogs(this.checked)" style="width: auto; margin-right: 8px;">
            <label for="saveLogs" style="display: inline; margin: 0; cursor: pointer;">开启日志保留 (./log/log.txt)</label>
        </div>

        <div id="message" class="message"></div>

        <!-- 二维码显示模态框 -->
        <div id="qrcodeModal" style="display: none; position: fixed; top: 0; left: 0; width: 100%; height: 100%; background-color: rgba(0,0,0,0.5); z-index: 1000; justify-content: center; align-items: center;">
            <div style="background: white; padding: 20px; border-radius: 8px; text-align: center;">
                <h3>访问二维码</h3>
                <div id="qrcodeContainer" style="margin: 20px 0;"></div>
                <p id="qrcodeUrl"></p>
                <button onclick="closeQRCodeModal()" style="padding: 8px 16px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer;">关闭</button>
            </div>
        </div>

        <div class="form-group">
            <label>运行日志:</label>
            <div class="log-area" id="logArea"></div>
        </div>
    </div>

    <script>
        // Wails 绑定桥接
        var wailsApp = null;
        
        // 等待 Wails 运行时加载
        function initWails() {
            if (window.go && window.go.main && window.go.main.App) {
                // 使用标准 Wails 绑定（优先）
                wailsApp = window.go.main.App;
            } else if (window.runtime && window.runtime.Call) {
                // 使用 Wails 运行时 API（新版）
                wailsApp = {
                    GetLocalIPs: function() {
                        return new Promise(function(resolve, reject) {
                            window.runtime.Call('GetLocalIPs', [], function(result) {
                                resolve(result);
                            });
                        });
                    },
                    StartServer: function(dir, ip, port) {
                        return new Promise(function(resolve, reject) {
                            window.runtime.Call('StartServer', [dir, ip, port], function(result) {
                                resolve(result);
                            });
                        });
                    },
                    StopServer: function() {
                        return new Promise(function(resolve, reject) {
                            window.runtime.Call('StopServer', [], function(result) {
                                resolve(result);
                            });
                        });
                    },
                    GetLogs: function() {
                        return new Promise(function(resolve, reject) {
                            window.runtime.Call('GetLogs', [], function(result) {
                                resolve(result);
                            });
                        });
                    },
                    ClearLogs: function() {
                        return new Promise(function(resolve, reject) {
                            window.runtime.Call('ClearLogs', [], function(result) {
                                resolve(result);
                            });
                        });
                    },
                    SelectDirectory: function() {
                        return new Promise(function(resolve, reject) {
                            window.runtime.Call('SelectDirectory', [], function(result) {
                                resolve(result);
                            });
                        });
                    }
                };
            } else {
                // 降级：使用 fetch API 调用后端
                console.warn('Wails 绑定不可用，使用 HTTP API');
                wailsApp = {
                    GetLocalIPs: async function() {
                        var response = await fetch('/api/getLocalIPs');
                        return await response.json();
                    },
                    StartServer: async function(dir, ip, port) {
                        var response = await fetch('/api/startServer', {
                            method: 'POST',
                            headers: {'Content-Type': 'application/json'},
                            body: JSON.stringify({dir: dir, ip: ip, port: port})
                        });
                        return await response.json();
                    },
                    StopServer: async function() {
                        var response = await fetch('/api/stopServer', {method: 'POST'});
                        return await response.json();
                    },
                    GetLogs: async function() {
                        var response = await fetch('/api/getLogs');
                        return await response.json();
                    },
                    ClearLogs: async function() {
                        await fetch('/api/clearLogs', {method: 'POST'});
                    },
                    SelectDirectory: async function() {
                        // 降级方案：返回空字符串，前端会使用prompt()
                        return '';
                    }
                };
            }
        }

        var ips = [];
        
        // 页面加载时初始化
        window.onload = async function() {
            initWails();
            
            // 等待一下确保绑定就绪
            await new Promise(function(resolve) { setTimeout(resolve, 100); });
            
            try {
                if (wailsApp && wailsApp.GetLocalIPs) {
                    ips = await wailsApp.GetLocalIPs();
                    var ipSelect = document.getElementById('ip');
                    ipSelect.innerHTML = '';
                    for (var i = 0; i < ips.length; i++) {
                        var ipInfo = ips[i];
                        var option = document.createElement('option');
                        option.value = ipInfo.ip;
                        option.text = ipInfo.ip + ' (' + ipInfo.name + ')';
                        ipSelect.appendChild(option);
                    }
                }
            } catch(e) {
                console.error('获取IP列表失败:', e);
            }
            updateLogs();
            setInterval(updateLogs, 1000);
        };

        function showMessage(text, isError) {
            var msg = document.getElementById('message');
            msg.className = 'message ' + (isError ? 'error' : 'success') + ' show';
            
            // 检查是否是启动成功消息（包含访问地址）
            if (!isError && text.indexOf('服务器启动成功！') !== -1 && text.indexOf('访问地址:') !== -1) {
                // 提取访问地址
                var urlMatch = text.match(/访问地址: (http:\/\/[^\n]+)/);
                if (urlMatch && urlMatch[1]) {
                    var url = urlMatch[1];
                    // 构建包含按钮的HTML
                    msg.innerHTML = text + 
                        '<div style="margin-top: 10px; display: flex; gap: 10px;">' +
                            '<button onclick="copyToClipboard(\'' + url + '\')" style="padding: 6px 12px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 12px;">' +
                                '复制链接' +
                            '</button>' +
                            '<button onclick="showQRCode(\'' + url + '\')" style="padding: 6px 12px; background: #28a745; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 12px;">' +
                                '二维码' +
                            '</button>' +
                        '</div>';
                } else {
                    msg.textContent = text;
                }
            } else {
                msg.textContent = text;
            }
            
            // 只有错误信息才自动消失，成功信息保持显示
            if (isError) {
                setTimeout(function() {
                    msg.classList.remove('show');
                }, 5000);
            }
        }
        
        // 复制到剪贴板功能
        function copyToClipboard(text) {
            // 检查是否使用了0.0.0.0作为监听地址
            if (text.indexOf('0.0.0.0') !== -1) {
                // 获取本机IP列表
                if (ips && ips.length > 0) {
                    // 过滤掉0.0.0.0，因为它不是一个可直接访问的地址
                    var validIps = [];
                    for (var i = 0; i < ips.length; i++) {
                        if (ips[i].ip !== '0.0.0.0') {
                            validIps.push(ips[i]);
                        }
                    }
                    
                    // 创建IP选择对话框
                    var ipOptions = '';
                    for (var i = 0; i < validIps.length; i++) {
                        var ipInfo = validIps[i];
                        ipOptions += (i + 1) + '. ' + ipInfo.ip + ' (' + ipInfo.name + ')\n';
                    }
                    
                    // 提示用户选择IP
                    var choice = prompt('请选择要使用的IP地址:\n' + ipOptions + '\n输入对应数字：');
                    
                    if (choice) {
                        var index = parseInt(choice) - 1;
                        
                        if (index >= 0 && index < validIps.length) {
                            var selectedIp = validIps[index].ip;
                            // 替换URL中的0.0.0.0为选择的IP
                            text = text.replace('0.0.0.0', selectedIp);
                        } else {
                            alert('无效的选择');
                            return;
                        }
                    } else {
                        return; // 用户取消选择
                    }
                } else {
                    alert('无法获取本机IP地址');
                    return;
                }
            }
            
            navigator.clipboard.writeText(text).then(function() {
                // 显示复制成功提示
                var msg = document.getElementById('message');
                var originalContent = msg.innerHTML;
                msg.innerHTML = '<span style="color: #28a745;">链接已复制到剪贴板！</span>' + originalContent;
                setTimeout(function() {
                    // 恢复原始内容
                    msg.innerHTML = originalContent;
                }, 2000);
            }).catch(function(err) {
                console.error('复制失败:', err);
                alert('复制失败，请手动复制');
            });
        }
        
        // 显示二维码
        function showQRCode(url) {
            // 检查是否使用了0.0.0.0作为监听地址
            if (url.indexOf('0.0.0.0') !== -1) {
                // 获取本机IP列表
                if (ips && ips.length > 0) {
                    // 过滤掉0.0.0.0，因为它不是一个可直接访问的地址
                    var validIps = [];
                    for (var i = 0; i < ips.length; i++) {
                        if (ips[i].ip !== '0.0.0.0') {
                            validIps.push(ips[i]);
                        }
                    }
                    
                    // 创建IP选择对话框
                    var ipOptions = '';
                    for (var i = 0; i < validIps.length; i++) {
                        var ipInfo = validIps[i];
                        ipOptions += (i + 1) + '. ' + ipInfo.ip + ' (' + ipInfo.name + ')\n';
                    }
                    
                    // 提示用户选择IP
                    var choice = prompt('请选择要使用的IP地址:\n' + ipOptions + '\n输入对应数字：');
                    
                    if (choice) {
                        var index = parseInt(choice) - 1;
                        
                        if (index >= 0 && index < validIps.length) {
                            var selectedIp = validIps[index].ip;
                            // 替换URL中的0.0.0.0为选择的IP
                            url = url.replace('0.0.0.0', selectedIp);
                        } else {
                            alert('无效的选择');
                            return;
                        }
                    } else {
                        return; // 用户取消选择
                    }
                } else {
                    alert('无法获取本机IP地址');
                    return;
                }
            }
            
            var modal = document.getElementById('qrcodeModal');
            var container = document.getElementById('qrcodeContainer');
            var urlElement = document.getElementById('qrcodeUrl');
            
            // 清空容器
            container.innerHTML = '';
            urlElement.textContent = url;
            
            // 使用本地API生成二维码图片
            var img = document.createElement('img');
            img.src = '/api/qrcode?data=' + encodeURIComponent(url);
            img.alt = 'QR Code';
            img.style.width = '200px';
            img.style.height = '200px';
            container.appendChild(img);
            
            // 显示模态框
            modal.style.display = 'flex';
        }
        
        // 关闭二维码模态框
        function closeQRCodeModal() {
            var modal = document.getElementById('qrcodeModal');
            modal.style.display = 'none';
        }

        async function browseDirectory() {
            try {
                if (wailsApp && wailsApp.SelectDirectory) {
                    var selectedDir = await wailsApp.SelectDirectory();
                    if (selectedDir) {
                        document.getElementById('dir').value = selectedDir;
                    }
                    // 不再使用降级方案，避免显示输入框
                    return;
                }
                // 如果没有SelectDirectory方法，则直接返回
                return;
            } catch (e) {
                console.error('选择目录失败:', e);
                // 出错时直接返回，不显示输入框
                return;
            }
        }

        async function start() {
            var dir = document.getElementById('dir').value.trim();
            var ip = document.getElementById('ip').value;
            var port = document.getElementById('port').value.trim();

            if (!dir || !ip || !port) {
                showMessage('请填写所有字段！', true);
                return;
            }

            try {
                if (!wailsApp || !wailsApp.StartServer) {
                    showMessage('Wails 绑定未初始化，请刷新页面重试', true);
                    return;
                }
                
                var result = await wailsApp.StartServer(dir, ip, port);
                if (result && result.success) {
                    showMessage(result.message, false);
                    document.getElementById('stopBtn').disabled = false;
                    document.querySelector('.btn-start').disabled = true;
                } else {
                    showMessage(result ? result.message : '启动失败', true);
                }
            } catch(e) {
                showMessage('启动失败: ' + (e.message || e), true);
                console.error('启动错误:', e);
            }
        }

        async function stop() {
            try {
                if (!wailsApp || !wailsApp.StopServer) {
                    showMessage('Wails 绑定未初始化', true);
                    return;
                }
                
                var result = await wailsApp.StopServer();
                if (result && result.success) {
                    showMessage(result.message, false);
                    document.getElementById('stopBtn').disabled = true;
                    document.querySelector('.btn-start').disabled = false;
                } else {
                    showMessage(result ? result.message : '停止失败', true);
                }
            } catch(e) {
                showMessage('停止失败: ' + (e.message || e), true);
                console.error('停止错误:', e);
            }
        }

        async function clearLog() {
            try {
                if (wailsApp && wailsApp.ClearLogs) {
                    await wailsApp.ClearLogs();
                }
                document.getElementById('logArea').innerHTML = '';
            } catch(e) {
                console.error('清空日志失败:', e);
            }
        }

        function toggleSaveLogs(enable) {
            fetch('/api/toggleSaveLogs', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ enable: enable })
            })
            .catch(error => showMessage('设置日志保存失败: ' + error, 'error'));
        }

        async function updateLogs() {
            try {
                if (wailsApp && wailsApp.GetLogs) {
                    var logs = await wailsApp.GetLogs();
                    var logArea = document.getElementById('logArea');
                    var logHTML = '';
                    for (var i = 0; i < logs.length; i++) {
                        logHTML += '<div class="log-line">' + escapeHtml(logs[i]) + '</div>';
                    }
                    logArea.innerHTML = logHTML;
                    logArea.scrollTop = logArea.scrollHeight;
                }
            } catch(e) {
                console.error('更新日志失败:', e);
            }
        }

        function escapeHtml(text) {
            var div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }
    </script>
</body>
</html>`
}
