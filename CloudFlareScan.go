package main

import (
	"bufio"
	"bytes"
	"context" // 必须导入 context 包
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------
// 数据结构定义
// ---------------------------------------------------------

type PingResult struct {
	IP      string
	Port    int
	Latency time.Duration // 延迟
	Loss    bool          // 是否丢包
}

type SpeedResult struct {
	IP      string
	Port    int
	Latency time.Duration
	Speed   float64 // MB/s
	Colo    string  // 地区代码
}

// Gist JSON 结构
type GistRequest struct {
	Description string              `json:"description"`
	Public      bool                `json:"public"`
	Files       map[string]GistFile `json:"files"`
}

type GistFile struct {
	Content string `json:"content"`
}

// ---------------------------------------------------------
// 全局配置变量
// ---------------------------------------------------------
var (
	file      = flag.String("f", "ip.txt", "IP段文件")
	outFile   = flag.String("o", "result.csv", "输出文件")
	maxThread = flag.Int("t", 100, "并发线程数")
	maxPing   = flag.Int("dt", 10, "延迟测速数量(即第一轮筛选多少个低延迟IP)")
	urlPath   = flag.String("url", "https://speed.cloudflare.com/__down?bytes=20000000", "测速下载地址")
	port      = flag.Int("p", 443, "测速端口")
	mode      = flag.Int("mode", 3, "模式: 1=全球Top10, 2=指定地区Top10, 3=每地区Top3")
)

// ---------------------------------------------------------
// 主程序
// ---------------------------------------------------------

func main() {
	flag.Parse() // 解析命令行参数

	// 1. 读取 IP 列表
	ips, err := loadIPs(*file)
	if err != nil {
		fmt.Println("[错误] 无法读取IP文件(ip.txt):", err)
		fmt.Println("请确保目录下有 ip.txt 文件，或使用 -f 指定文件。")
		return
	}
	fmt.Printf("已加载 %d 个 IP，开始并发测速 (线程数: %d)...\n", len(ips), *maxThread)

	// 2. 第一阶段：TCP 延迟测速 (Ping)
	pingResults := startPingTest(ips)
	if len(pingResults) == 0 {
		fmt.Println("未找到有效 IP (全部Ping失败)，程序退出。")
		return
	}

	// 截取延迟最低的前 N 个进行下载测速
	limit := *maxPing
	if limit > len(pingResults) {
		limit = len(pingResults)
	}
	topPingResults := pingResults[:limit]
	fmt.Printf("\n延迟筛选完成，选取延迟最低的 %d 个 IP 进行下载测速...\n", limit)

	// 3. 第二阶段：下载测速 & 获取地区
	speedResults := startSpeedTest(topPingResults)

	// 4. 第三阶段：根据模式筛选结果
	var finalResults []SpeedResult
	fmt.Println("\n正在根据策略筛选结果...")

	if *mode == 3 {
		fmt.Println(">> 模式启用：保留每个地区(Colo)最快的前 3 名")
		finalResults = filterRegionTop3(speedResults)
	} else if *mode == 2 {
		fmt.Println(">> 模式启用：全局 Top 10 (模式2需指定地区代码，此处简化为Top10)")
		finalResults = filterTop10(speedResults)
	} else {
		fmt.Println(">> 模式启用：全局 Top 10")
		finalResults = filterTop10(speedResults)
	}

	// 5. 输出结果
	printTable(finalResults)

	// 6. 保存到 CSV
	saveToCSV(finalResults, *outFile)
	fmt.Printf("\n结果已保存到本地: %s\n", *outFile)

	// 7. Gist 上传逻辑 (交互式)
	handleGistUpload(finalResults)

	fmt.Println("\n程序结束，按回车退出...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// ---------------------------------------------------------
// 核心逻辑实现
// ---------------------------------------------------------

func loadIPs(filename string) ([]string, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(content), "\n")
	var ips []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 简单过滤空行和注释
		if line != "" && !strings.HasPrefix(line, "#") {
			ips = append(ips, line)
		}
	}
	return ips, nil
}

func startPingTest(ips []string) []PingResult {
	var wg sync.WaitGroup
	results := make(chan PingResult, len(ips))
	guard := make(chan struct{}, *maxThread)

	for _, ip := range ips {
		wg.Add(1)
		guard <- struct{}{}
		go func(targetIP string) {
			defer wg.Done()
			defer func() { <-guard }()

			start := time.Now()
			// 1秒超时
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetIP, *port), 1*time.Second)
			if err == nil {
				conn.Close()
				results <- PingResult{IP: targetIP, Port: *port, Latency: time.Since(start), Loss: false}
			}
		}(ip)
	}
	wg.Wait()
	close(results)

	var sorted []PingResult
	for r := range results {
		sorted = append(sorted, r)
	}
	// 按延迟升序
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Latency < sorted[j].Latency
	})
	return sorted
}

func startSpeedTest(pings []PingResult) []SpeedResult {
	var wg sync.WaitGroup
	results := make(chan SpeedResult, len(pings))
	// 下载测速并发不宜过高，防止带宽跑满导致结果不准，固定为 5
	guard := make(chan struct{}, 5)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout: 3 * time.Second,
			DisableKeepAlives:   true,
			TLSClientConfig:     nil, // 忽略证书校验
		},
	}

	fmt.Println("正在进行下载测速及地区检测...")

	for _, p := range pings {
		wg.Add(1)
		guard <- struct{}{}
		go func(pr PingResult) {
			defer wg.Done()
			defer func() { <-guard }()

			req, _ := http.NewRequest("GET", *urlPath, nil)

			// 强制解析 TCP 连接到指定 IP
			dialer := &net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 30 * time.Second,
			}
			transport := &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", pr.IP, pr.Port))
				},
				DisableKeepAlives: true,
				TLSClientConfig:   nil,
			}
			client.Transport = transport

			start := time.Now()
			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				written, _ := io.Copy(io.Discard, resp.Body)
				duration := time.Since(start)

				speed := float64(written) / 1024 / 1024 / duration.Seconds() // MB/s

				// 尝试获取地区
				colo := "UNK"
				ray := resp.Header.Get("CF-RAY")
				if ray != "" && strings.Contains(ray, "-") {
					parts := strings.Split(ray, "-")
					colo = parts[len(parts)-1]
				}

				results <- SpeedResult{
					IP: pr.IP, Port: pr.Port, Latency: pr.Latency, Speed: speed, Colo: colo,
				}
				fmt.Print(".") // 进度点
			}
		}(p)
	}
	wg.Wait()
	close(results)
	fmt.Println("\n下载测速完成！")

	var list []SpeedResult
	for r := range results {
		list = append(list, r)
	}
	return list
}

// ---------------------------------------------------------
// 筛选逻辑
// ---------------------------------------------------------

func filterTop10(nodes []SpeedResult) []SpeedResult {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Speed > nodes[j].Speed
	})
	if len(nodes) > 10 {
		return nodes[:10]
	}
	return nodes
}

func filterRegionTop3(nodes []SpeedResult) []SpeedResult {
	// 1. 先按速度降序
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Speed > nodes[j].Speed
	})

	coloMap := make(map[string]int)
	var filtered []SpeedResult

	for _, n := range nodes {
		if coloMap[n.Colo] < 3 {
			filtered = append(filtered, n)
			coloMap[n.Colo]++
		}
	}

	// 2. 结果按地区名排序
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Colo < filtered[j].Colo
	})

	return filtered
}

// ---------------------------------------------------------
// 输出与上传
// ---------------------------------------------------------

func saveToCSV(nodes []SpeedResult, filename string) {
	var buf bytes.Buffer
	buf.WriteString("IP,端口,地区,延迟(ms),速度(MB/s)\n")
	for _, n := range nodes {
		buf.WriteString(fmt.Sprintf("%s,%d,%s,%d,%.2f\n",
			n.IP, n.Port, n.Colo, n.Latency.Milliseconds(), n.Speed))
	}
	ioutil.WriteFile(filename, buf.Bytes(), 0644)
}

func generateTxtContent(nodes []SpeedResult) string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("%-16s | %-5s | %-10s | %s\n", "IP Address", "Colo", "Speed", "Latency"))
	buf.WriteString(strings.Repeat("-", 60) + "\n")
	for _, n := range nodes {
		buf.WriteString(fmt.Sprintf("%-16s | %-5s | %-10.2f | %dms\n",
			n.IP, n.Colo, n.Speed, n.Latency.Milliseconds()))
	}
	return buf.String()
}

func printTable(nodes []SpeedResult) {
	fmt.Println(generateTxtContent(nodes))
}

func handleGistUpload(nodes []SpeedResult) {
	fmt.Println("\n[可选] 上传测速结果到 GitHub Gist")
	fmt.Println("请输入 Gist Token (权限: gist)，直接回车则跳过:")
	fmt.Print("> ")

	reader := bufio.NewReader(os.Stdin)
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)

	if token == "" {
		fmt.Println("跳过上传。")
		return
	}

	fmt.Println("请输入 Gist ID (用于更新现有Gist，留空回车则新建):")
	fmt.Print("> ")
	id, _ := reader.ReadString('\n')
	id = strings.TrimSpace(id)

	content := generateTxtContent(nodes)
	err := uploadToGist(token, id, content)
	if err != nil {
		fmt.Printf("上传失败: %v\n", err)
	} else {
		fmt.Println("上传成功！")
	}
}

func uploadToGist(token, id, content string) error {
	url := "https://api.github.com/gists"
	method := "POST"
	if id != "" {
		url += "/" + id
		method = "PATCH"
	}

	payload := GistRequest{
		Description: "Cloudflare Scan Result",
		Public:      false,
		Files: map[string]GistFile{
			"result.txt": {Content: content},
		},
	}

	data, _ := json.Marshal(payload)
	req, _ := http.NewRequest(method, url, bytes.NewBuffer(data))
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return fmt.Errorf("Status: %s, Body: %s", resp.Status, string(body))
}
