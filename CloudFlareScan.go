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
	"time"
)

// ----------------------
// 1. 基础配置与结构体
// ----------------------

type Node struct {
	IP      string
	Colo    string        // 地区代码 (如 HKG, SJC)
	Latency time.Duration // 延迟
	Speed   float64       // 下载速度 (MB/s)
}

// Gist 结构体用于构建 JSON 请求
type GistRequest struct {
	Description string              `json:"description"`
	Public      bool                `json:"public"`
	Files       map[string]GistFile `json:"files"`
}

type GistFile struct {
	Content string `json:"content"`
}

var (
	inputReader = bufio.NewReader(os.Stdin)
)

// ----------------------
// 2. 主程序入口
// ----------------------

func main() {
	printBanner()

	// --- 步骤 1：选择测速模式 ---
	mode := showMenu()

	// --- 步骤 2：执行测速 (获取数据) ---
	// 在实际项目中，这里会调用真实的 scanning/ping 逻辑
	// 这里使用 mockScanning() 模拟数据返回，以便代码可直接运行测试
	allNodes := mockScanning() 

	// --- 步骤 3：根据模式处理结果 ---
	var finalResults []Node
	
	switch mode {
	case "1":
		fmt.Println("\n[模式1] 正在筛选全球最快的 10 个 IP...")
		finalResults = filterTop10(allNodes)
	case "2":
		targetColo := askForColo()
		fmt.Printf("\n[模式2] 正在筛选地区 [%s] 最快的 10 个 IP...\n", targetColo)
		finalResults = filterRegionTop10(allNodes, targetColo)
	case "3":
		fmt.Println("\n[模式3] 正在筛选每个地区最快的前 3 个 IP...")
		finalResults = filterRegionTop3(allNodes)
	default:
		fmt.Println("输入错误，默认执行模式 1")
		finalResults = filterTop10(allNodes)
	}

	// --- 步骤 4：输出与保存结果 ---
	outputString := generateOutput(finalResults)
	printTable(finalResults)
	
	// 保存到本地 CSV
	os.WriteFile("result.csv", []byte(generateCSV(finalResults)), 0644)
	fmt.Println("\n本地结果已保存至: result.csv")

	// --- 步骤 5：Gist 上传选项 ---
	askAndUploadGist(outputString)

	fmt.Println("\n所有任务完成，按回车键退出程序...")
	inputReader.ReadString('\n')
}

// ----------------------
// 3. 核心逻辑函数
// ----------------------

// 模式 1：全球最快前 10
func filterTop10(nodes []Node) []Node {
	// 按速度降序
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Speed > nodes[j].Speed
	})
	if len(nodes) > 10 {
		return nodes[:10]
	}
	return nodes
}

// 模式 2：指定地区前 10
func filterRegionTop10(nodes []Node, targetColo string) []Node {
	var filtered []Node
	targetColo = strings.ToUpper(targetColo)
	
	for _, n := range nodes {
		if strings.ToUpper(n.Colo) == targetColo {
			filtered = append(filtered, n)
		}
	}
	// 排序
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Speed > filtered[j].Speed
	})
	if len(filtered) > 10 {
		return filtered[:10]
	}
	return filtered
}

// [新增功能] 模式 3：每个地区保留前 3 名
func filterRegionTop3(nodes []Node) []Node {
	// 1. 先按速度全局排序
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Speed > nodes[j].Speed
	})

	// 2. 遍历并计数
	coloCount := make(map[string]int)
	var filtered []Node

	for _, n := range nodes {
		if coloCount[n.Colo] < 3 {
			filtered = append(filtered, n)
			coloCount[n.Colo]++
		}
	}

	// 3. 为了展示美观，最后按地区名称排个序
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Colo < filtered[j].Colo
	})
	
	return filtered
}

// ----------------------
// 4. 交互与上传逻辑
// ----------------------

func showMenu() string {
	fmt.Println("\n请选择测速模式：")
	fmt.Println("1. 全面测速 - 保留全球最快前 10 名")
	fmt.Println("2. 指定地区 - 保留该地区最快前 10 名")
	fmt.Println("3. [新增] 全面测速 - 保留每个地区最快前 3 名")
	fmt.Print("\n请输入序号 (1-3): ")
	
	input, _ := inputReader.ReadString('\n')
	return strings.TrimSpace(input)
}

func askForColo() string {
	fmt.Print("请输入目标地区代码 (例如 HKG, JP, US): ")
	input, _ := inputReader.ReadString('\n')
	return strings.TrimSpace(input)
}

// 处理 Gist 上传
func askAndUploadGist(content string) {
	fmt.Println("\n------------------------------------------------")
	fmt.Println("   GitHub Gist 结果同步 (文件名为 result.txt)")
	fmt.Println("------------------------------------------------")
	fmt.Println("请输入您的 GitHub Gist Token (权限: gist)")
	fmt.Print("如果不需要上传，请直接按回车跳过: ")

	token, _ := inputReader.ReadString('\n')
	token = strings.TrimSpace(token)

	if token == "" {
		fmt.Println("已跳过上传。")
		return
	}

	fmt.Println("正在上传 result.txt 到 GitHub Gist...")
	err := uploadToGist(token, content)
	if err != nil {
		fmt.Printf("上传失败: %v\n", err)
	} else {
		fmt.Println("上传成功！请登录 GitHub Gist 查看。")
	}
}

// 上传具体实现
func uploadToGist(token, content string) error {
	url := "https://api.github.com/gists"
	
	// 构建 JSON
	payload := GistRequest{
		Description: "Cloudflare Scan Result - Auto Upload",
		Public:      false, // 默认为私有
		Files: map[string]GistFile{
			"result.txt": {Content: content}, // 按照要求，文件名为 result.txt
		},
	}
	
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API 响应错误: %s - %s", resp.Status, string(body))
	}

	// 解析返回以获取 URL (可选)
	return nil
}

// ----------------------
// 5. 辅助工具函数
// ----------------------

func generateOutput(nodes []Node) string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("%-15s | %-6s | %-10s | %s\n", "IP地址", "地区", "速度(MB/s)", "延迟(ms)"))
	buf.WriteString(strings.Repeat("-", 55) + "\n")
	for _, n := range nodes {
		buf.WriteString(fmt.Sprintf("%-15s | %-6s | %-10.2f | %d\n", n.IP, n.Colo, n.Speed, n.Latency.Milliseconds()))
	}
	return buf.String()
}

func generateCSV(nodes []Node) string {
	var buf bytes.Buffer
	buf.WriteString("IP,Colo,Speed(MB/s),Latency(ms)\n")
	for _, n := range nodes {
		buf.WriteString(fmt.Sprintf("%s,%s,%.2f,%d\n", n.IP, n.Colo, n.Speed, n.Latency.Milliseconds()))
	}
	return buf.String()
}

func printTable(nodes []Node) {
	fmt.Println(generateOutput(nodes))
}

func printBanner() {
	fmt.Println("##################################################")
	fmt.Println("#           CloudFlare IP 优选工具 (Pro)         #")
	fmt.Println("#         集成地区智能筛选与 Gist 云端同步       #")
	fmt.Println("##################################################")
}

// 模拟扫描数据 (替换原项目中的真实扫描逻辑)
func mockScanning() []Node {
	fmt.Println("正在初始化 IP 库...")
	time.Sleep(500 * time.Millisecond)
	fmt.Println("正在进行并发测速 (模拟)...")
	time.Sleep(1 * time.Second)
	
	// 模拟返回一组杂乱的数据
	return []Node{
		{"104.1.1.1", "HKG", 20 * time.Millisecond, 15.5},
		{"104.1.1.2", "HKG", 22 * time.Millisecond, 20.1}, // HKG 1
		{"104.1.1.3", "HKG", 25 * time.Millisecond, 18.0}, // HKG 2
		{"104.1.1.4", "HKG", 28 * time.Millisecond, 14.0}, // HKG 3
		{"104.1.1.5", "HKG", 30 * time.Millisecond, 5.0},  // HKG 4 (应被剔除)
		{"172.67.1.1", "SJC", 140 * time.Millisecond, 30.0}, // SJC 1
		{"172.67.1.2", "SJC", 142 * time.Millisecond, 28.0}, // SJC 2
		{"172.67.1.3", "SJC", 145 * time.Millisecond, 25.0}, // SJC 3
		{"172.67.1.4", "SJC", 148 * time.Millisecond, 10.0}, // SJC 4 (应被剔除)
		{"1.1.1.1", "NRT", 60 * time.Millisecond, 12.0},
		{"1.0.0.1", "LAX", 130 * time.Millisecond, 22.5},
	}
}
