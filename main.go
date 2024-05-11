package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// 定义一个结构体来表示将要返回的JSON数据
type JsonResponse struct {
	Message string `json:"message"`
}

func main() {
	// 为/api/get路由定义处理函数,返回字符响应
	http.HandleFunc("/api/get", func(w http.ResponseWriter, r *http.Request) {
		// 写入应答
		io.WriteString(w, "yes")
	})
	// 为/api/getjson路由定义处理函数，返回JSON响应
	http.HandleFunc("/api/getjson", func(w http.ResponseWriter, r *http.Request) {
		// 设置响应的内容类型为application/json
		w.Header().Set("Content-Type", "application/json")

		// 实例化JsonResponse结构体
		response := JsonResponse{
			Message: "Hello, this is JSON response",
		}

		// 编码并写入JSON响应
		json.NewEncoder(w).Encode(response)
	})

	// 静态资源服务器，设置静态文件的目录
	staticDir := http.Dir("./public")
	// 使用FileServer处理静态文件请求
	http.Handle("/", http.FileServer(staticDir))

	// 设置监听端口
	addr := ":8088"
	log.Printf("服务端正在监听端口 %s，请在同目录下的public/目录里修改静态资源哦!", addr)

	// 开始监听并提供服务
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
