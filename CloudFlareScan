package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ----------------------
// 数据结构定义
// ----------------------

// Node 代表一个测速节点的结果
type Node struct {
	IP      string
	Colo    string        // 地区代码 (如 HKG, SJC)
	Latency time.Duration // 延迟
	Speed   float64       // 下载速度 (MB/s)
}

// GistRequest 用于构建上传到 GitHub API 的 JSON 结构
type GistRequest struct {
	Description string              `json:"description"`
	Public      bool                `json:"public"`
	Files       map[string]GistFile `json:"files"`
}

type GistFile struct {
	Content string `json:"content"`
}

// ----------------------
// 主程序入口
// ----------------------

func main() {
	fmt.Println("--- CloudFlare IP 优选工具 (增强版) ---")
	fmt.Println("功能：地区优选(前3名) + Gist上传")

	// 1. 获取 IP 列表 (这里为了演示，模拟了一些数据，实际使用中请从 ip.txt 读取或扫描)
	// 在实际代码中，这里应该是调用 scanning 逻辑
	results := mockScanning() 

	// 2. 处理结果：按地区分组，取前3名
	finalResults := processRegionalResults(results)

	// 3. 打印结果到控制台
	printResults(finalResults)

	// 4. 保存结果到本地文件
	saveFile := "result.csv"
	content := generateCSV(finalResults)
	os.WriteFile(saveFile, []byte(content), 0644)
	fmt.Printf("\n结果已保存到本地: %s\n", saveFile)

	// 5. 交互式上传 Gist
	handleGistUpload(content)
	
	fmt.Println("\n程序运行结束，按回车键退出...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// ----------------------
// 核心逻辑函数
// ----------------------

// processRegionalResults: 核心逻辑 - 地区分组保留前3
func processRegionalResults(nodes []Node) []Node {
	fmt.Println("\n正在进行智能筛选：保留每个地区(Colo)速度最快的前 3 名...")

	// 先按速度降序排序 (速度快的排前面)
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Speed > nodes[j].Speed
	})

	regionCount := make(map[string]int)
	var filtered []Node

	for _, node := range nodes {
		// 如果该地区还没有存满3个，则加入
		if regionCount[node.Colo] < 3 {
			filtered = append(filtered, node)
			regionCount[node.Colo]++
		}
	}
	
	// 为了美观，最终输出可以按地区排序
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Colo < filtered[j].Colo
	})

	return filtered
}

// handleGistUpload: 处理交互输入和上传
func handleGistUpload(content string) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n--- GitHub Gist 上传设置 ---")
	fmt.Println("提示：Token 需要在 GitHub Settings -> Developer settings 申请 (权限: gist)")
	
	fmt.Print("请输入 Gist Token (直接回车跳过上传): ")
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)

	if token == "" {
		fmt.Println("未输入 Token，跳过上传。")
		return
	}

	fmt.Print("请输入 Gist ID (直接回车将创建新 Gist): ")
	gistID, _ := reader.ReadString('\n')
	gistID = strings.TrimSpace(gistID)

	fmt.Println("正在上传中，请稍候...")
	
	// 执行上传
	err := uploadToGist(token, gistID, "cf_speed_result.csv", content)
	if err != nil {
		fmt.Printf("上传失败: %v\n", err)
	} else {
		fmt.Println("上传成功！数据已同步到 GitHub Gist。")
	}
}

// uploadToGist: 调用 GitHub API
func uploadToGist(token, gistID, filename, content string) error {
	url := "https://api.github.com/gists"
	method := "POST"

	// 如果提供了 ID，则是更新操作 (PATCH)
	if gistID != "" {
		url = url + "/" + gistID
		method = "PATCH"
	}

	// 构建 JSON
	payload := GistRequest{
		Description: "Cloudflare SpeedTest Result (Auto Upload)",
		Public:      false,
		Files: map[string]GistFile{
			filename: {Content: content},
		},
	}
	
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// 创建请求
	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 尝试解析返回的 HTML URL
		var result map[string]interface{}
		json.Unmarshal(body, &result)
		if htmlUrl, ok := result["html_url"]; ok {
			fmt.Printf("Gist 链接: %s\n", htmlUrl)
		}
		if id, ok := result["id"]; ok {
			fmt.Printf("Gist ID  : %s (请妥善保存以便下次更新)\n", id)
		}
		return nil
	}

	return fmt.Errorf("API 错误: %s - %s", resp.Status, string(body))
}

// ----------------------
// 辅助函数
// ----------------------

func generateCSV(nodes []Node) string {
	var buf bytes.Buffer
	buf.WriteString("IP,地区(Colo),速度(MB/s),延迟(ms)\n")
	for _, n := range nodes {
		buf.WriteString(fmt.Sprintf("%s,%s,%.2f,%d\n", n.IP, n.Colo, n.Speed, n.Latency.Milliseconds()))
	}
	return buf.String()
}

func printResults(nodes []Node) {
	fmt.Printf("\n%-16s | %-6s | %-12s | %s\n", "IP地址", "地区", "下载速度", "延迟")
	fmt.Println(strings.Repeat("-", 50))
	for _, n := range nodes {
		fmt.Printf("%-16s | %-6s | %-10.2fMB/s | %dms\n", n.IP, n.Colo, n.Speed, n.Latency.Milliseconds())
	}
}

// mockScanning: 模拟测速过程 (实际代码中应替换为真实的 Ping/Download 逻辑)
func mockScanning() []Node {
	// 这里模拟生成 10 个数据，包含重复的地区，用于测试筛选逻辑
	fmt.Println("模拟测速中...")
	time.Sleep(1 * time.Second)
	return []Node{
		{"1.1.1.1", "HKG", 50 * time.Millisecond, 15.5},
		{"1.0.0.1", "HKG", 52 * time.Millisecond, 18.2}, // HKG No.1
		{"104.16.1.1", "HKG", 48 * time.Millisecond, 12.0},
		{"104.16.1.2", "HKG", 60 * time.Millisecond, 5.0}, // HKG 第4名，应该被剔除
		{"172.67.1.1", "SJC", 150 * time.Millisecond, 20.0}, // SJC No.1
		{"172.67.1.2", "SJC", 155 * time.Millisecond, 19.0},
		{"104.20.1.1", "NRT", 80 * time.Millisecond, 10.0},
		{"104.20.1.2", "NRT", 85 * time.Millisecond, 9.5},
		{"104.20.1.3", "NRT", 90 * time.Millisecond, 8.0},
		{"104.20.1.4", "NRT", 95 * time.Millisecond, 1.0}, // NRT 第4名，应该被剔除
	}
}
