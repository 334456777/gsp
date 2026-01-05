// Package main 提供视频静态片段分析工具，用于检测视频中的静止画面区域，
// 并使用 Google Gemini AI 分析视觉内容与音频内容的相关性。
//
// 工具的工作流程分为三个主要阶段：
//  1. 从 .pb.zst 文件加载预分析的视频数据（Protocol Buffers + Zstandard 压缩）
//  2. 从检测到的静态片段中提取图片帧和音频片段
//  3. 使用 Google Gemini AI 分析视觉与音频内容的匹配度
//
// 使用方法：
//
//	gsp                              # 使用 .pb.zst 文件中保存的推荐阈值
//	gsp <threshold>                  # 指定自定义阈值
//	gsp <threshold> <min_duration>   # 指定阈值和最小时长
//
// 示例：
//
//	gsp           # 使用输入文件内含的推荐阈值和默认 20 秒最小时长
//	gsp 500       # 使用阈值 500 和默认 20 秒最小时长
//	gsp 500 15    # 使用阈值 500 和 15 秒最小时长
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gobsnip/proto"

	"github.com/google/generative-ai-go/genai"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/api/option"
	pb "google.golang.org/protobuf/proto"
)

// Config 表示从 config.json 加载的应用程序配置。
// 包含访问 Gemini AI 服务所需的 API 凭证和设置。
type Config struct {
	// GeminiAPIKey 是访问 Google Gemini AI 服务的 API 密钥
	GeminiAPIKey string `json:"gemini_api_key"`

	// GeminiModelName 指定要使用的 Gemini 模型（例如 "gemini-1.5-pro"）
	GeminiModelName string `json:"gemini_model_name"`

	// GeminiRPM 设置每分钟请求数限制（Requests Per Minute）
	GeminiRPM int `json:"gemini_rpm"`
}

// loadConfig 从 config.json 文件读取并解析配置。
// 如果文件不存在或解析失败，返回错误。
//
// 返回值：
//   - *Config: 解析后的配置对象
//   - error: 读取或解析过程中的错误
func loadConfig() (*Config, error) {
	data, err := os.ReadFile("config.json")
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ---------------------------------------------------------
// 1. 数据结构
// ---------------------------------------------------------

// TimeRange 表示视频中的连续时间段。
// Start 和 End 都以秒为单位，从视频开始处计算。
type TimeRange struct {
	// Start 是起始时间（秒）
	Start float64

	// End 是结束时间（秒）
	End float64
}

// FilePair 表示提取的图片和音频文件对及其元数据。
// 每个 FilePair 对应视频中的一个静态片段。
type FilePair struct {
	// GroupIndex 是该片段的顺序编号（从 1 开始）
	GroupIndex int

	// VideoBaseName 是源视频文件的基础名称（不含扩展名）
	VideoBaseName string

	// ImagePath 是提取的图片文件的完整路径
	ImagePath string

	// AudioPath 是提取的音频文件的完整路径
	AudioPath string

	// ImageName 是提取的图片文件名
	ImageName string

	// AudioName 是提取的音频文件名
	AudioName string

	// DurationSec 是该片段的持续时间（秒）
	DurationSec float64
}

// GeminiResponse 表示 Gemini AI 返回的结构化响应，
// 包含对图片、音频及其相关性的分析结果。
type GeminiResponse struct {
	// GroupIndex 标识该分析属于哪个片段
	GroupIndex string `json:"group_index"`

	// ImageAnalysis 包含视觉内容的分析结果
	ImageAnalysis struct {
		// Filename 是被分析的图片文件名
		Filename string `json:"filename"`

		// VisualElements 描述图片中的视觉内容
		VisualElements string `json:"visual_elements"`
	} `json:"image_analysis"`

	// AudioAnalysis 包含音频内容的分析结果
	AudioAnalysis struct {
		// Filename 是被分析的音频文件名
		Filename string `json:"filename"`

		// Content 描述音频中表达的主题和情感
		Content string `json:"content"`
	} `json:"audio_analysis"`

	// CorrelationAnalysis 描述视觉和音频内容的匹配程度
	CorrelationAnalysis struct {
		// Description 详细说明视觉与音频的相关性及原因
		Description string `json:"description"`

		// Percentage 是相关度百分比（例如 "85%"）
		Percentage string `json:"percentage"`
	} `json:"correlation_analysis"`
}

// ---------------------------------------------------------
// 2. 主程序
// ---------------------------------------------------------

func main() {
	// --- 参数解析 ---
	args := os.Args[1:]

	printUsageAndExit := func() {
		fmt.Println(">> 用法:")
		fmt.Println("   gsp                              # 使用数据文件中保存的推荐阈值")
		fmt.Println("   gsp <threshold>                  # 指定自定义阈值")
		fmt.Println("   gsp <threshold> <min_duration>   # 指定阈值和最小时长")
		fmt.Println()
		fmt.Println(">> 示例:")
		fmt.Println("   gsp           # 使用推荐阈值和默认 20 秒最小时长")
		fmt.Println("   gsp 500       # 使用阈值 500 和默认 20 秒最小时长")
		fmt.Println("   gsp 500 15    # 使用阈值 500 和 15 秒最小时长")
		os.Exit(1)
	}

	var threshold float64 = -1        // -1 表示使用数据文件中的推荐值
	var minDurationSec float64 = 20.0 // 20.0 是默认最小连续区间

	// 解析命令行参数
	if len(args) > 2 {
		printUsageAndExit()
	}

	if len(args) >= 1 {
		if val, err := strconv.ParseFloat(args[0], 64); err == nil && val > 0 {
			threshold = val
		} else {
			fmt.Printf(">> 错误: 阈值参数不合法: %s\n", args[0])
			printUsageAndExit()
		}
	}

	if len(args) == 2 {
		if val, err := strconv.ParseFloat(args[1], 64); err == nil && val > 0 {
			minDurationSec = val
		} else {
			fmt.Printf(">> 错误: 时长参数不合法: %s\n", args[1])
			printUsageAndExit()
		}
	}

	// --- 单文件检测逻辑 ---
	targetDataFile := findDataFileInCurrentDir()
	if targetDataFile == "" {
		fmt.Println(">> 错误: 当前目录未找到 .pb.zst 文件")
		os.Exit(1)
	}

	// --- 处理单个文件 ---
	pairs, err := processDataFile(targetDataFile, threshold, minDurationSec)
	if err != nil {
		log.Fatalf(">> 处理文件失败: %v", err)
	}

	// --- Gemini 分析 ---
	if len(pairs) > 0 {
		askAndRunGemini(pairs, targetDataFile)
	} else {
		fmt.Println(">> 没有生成任何片段, 跳过 Gemini 分析")
	}
}

// findDataFileInCurrentDir 在当前目录中查找第一个 .pb.zst 文件（按字母顺序排序）。
// 如果未找到 .pb.zst 文件，返回空字符串。
//
// 返回值：
//   - string: 找到的 .pb.zst 文件名，如果未找到则返回空字符串
func findDataFileInCurrentDir() string {
	files, err := os.ReadDir(".")
	if err != nil {
		return ""
	}

	// 排序，确保每次运行选中的是同一个文件
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		fileName := file.Name()
		if strings.HasSuffix(strings.ToLower(fileName), ".pb.zst") {
			return fileName
		}
	}
	return ""
}

// processDataFile 从 .pb.zst 文件加载分析数据，检测静态片段，
// 并提取对应的图片和音频片段。
//
// 参数：
//   - dataPath: .pb.zst 文件的路径
//   - threshold: 检测静态片段的阈值，-1 表示使用文件中的推荐值
//   - minDuration: 静态片段的最小持续时间（秒）
//
// 返回值：
//   - []FilePair: 提取的文件对及其元数据的切片
//   - error: 处理过程中的错误
func processDataFile(dataPath string, threshold, minDuration float64) ([]FilePair, error) {
	var generatedPairs []FilePair

	fmt.Printf(">> 加载分析数据: %s\n", dataPath)

	// A. 读取并解压 pb.zst
	data, err := loadAnalysisResult(dataPath)
	if err != nil {
		return nil, err
	}

	if threshold < 0 {
		threshold = data.SuggestedThreshold
	}

	fmt.Printf("   -> 视频源: %s\n", filepath.Base(data.VideoFile))
	fmt.Printf("   -> 使用设定值: 阈值 = %.0f 最小时长 = %.0fs\n", threshold, minDuration)

	ranges := generateStaticRanges(data.DiffCounts, threshold, minDuration, data.Fps)

	if len(ranges) == 0 {
		fmt.Printf("   -> 未发现 %.0fs 以上的静止片段 (阈值: %.0f)\n", minDuration, threshold)
		return nil, nil
	}

	// --- 打印详细时间表 ---
	fmt.Printf("   -> 符合条件的片段: %d 个\n", len(ranges))
	fmt.Println("      [组号] 开始时间  -  结束时间      (时长)")
	for i, r := range ranges {
		startStr := formatTime(r.Start)
		endStr := formatTime(r.End)
		durStr := formatTime(r.End - r.Start)
		fmt.Printf("      [%02d] %s - %s (%s)\n", i+1, startStr, endStr, durStr)
	}

	// C. 准备输出目录
	baseNameNoExt := strings.TrimSuffix(filepath.Base(dataPath), ".pb.zst")
	outputDir := "output_" + baseNameNoExt
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}

	// D. 确定视频路径
	videoPath := data.VideoFile
	if _, err := os.Stat(videoPath); os.IsNotExist(err) {
		localVideo := filepath.Base(videoPath)
		if _, err := os.Stat(localVideo); err == nil {
			videoPath = localVideo
		} else {
			return nil, fmt.Errorf("找不到视频文件: %s (请确保视频在当前目录)", videoPath)
		}
	}

	fmt.Printf("   -> 开始提取图片和音频...\n")

	// E. 并发执行 FFmpeg
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 4)
	videoBaseName := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))

	for i, r := range ranges {
		wg.Add(1)
		sem <- struct{}{}

		groupIndex := i + 1

		go func(idx int, tr TimeRange) {
			defer wg.Done()
			defer func() { <-sem }()

			// 1. 图片处理
			snapTime := tr.Start + 2.0
			if snapTime > tr.End {
				snapTime = tr.Start + (tr.End-tr.Start)/2
			}
			imgName := fmt.Sprintf("%s-%d-1.jpg", videoBaseName, idx)
			imgPath := filepath.Join(outputDir, imgName)

			if _, err := os.Stat(imgPath); err != nil {
				_ = extractFrame(videoPath, imgPath, snapTime)
			}

			// 2. 音频处理
			audioName := fmt.Sprintf("%s-%d-2.mp3", videoBaseName, idx)
			audioPath := filepath.Join(outputDir, audioName)

			if _, err := os.Stat(audioPath); err != nil {
				_ = extractAudio(videoPath, audioPath, tr.Start, tr.End)
			}

			mu.Lock()
			generatedPairs = append(generatedPairs, FilePair{
				GroupIndex:    idx,
				VideoBaseName: videoBaseName,
				ImagePath:     imgPath,
				AudioPath:     audioPath,
				ImageName:     imgName,
				AudioName:     audioName,
				DurationSec:   tr.End - tr.Start,
			})
			mu.Unlock()

		}(groupIndex, r)
	}

	wg.Wait()
	fmt.Printf("   -> 完成, 输出至: %s/\n", outputDir)
	return generatedPairs, nil
}

// ---------------------------------------------------------
// 3. Gemini 分析模块
// ---------------------------------------------------------

// askAndRunGemini 询问用户是否进行 Gemini 分析，如果确认则执行分析并生成 Markdown 报告。
//
// 该函数会：
//  1. 显示 Token 使用预估
//  2. 询问用户确认
//  3. 加载 API 配置
//  4. 初始化 Gemini 客户端
//  5. 逐个分析文件对
//  6. 生成包含所有分析结果的 Markdown 报告
//
// 参数：
//   - pairs: 要分析的文件对列表
//   - originalDataFileName: 原始 .pb.zst 文件名，用于生成报告文件名
func askAndRunGemini(pairs []FilePair, originalDataFileName string) {
	fmt.Println(">> Gemini 分析准备")

	// --- Token 预估 ---
	const (
		ImageTokenCost   = 258
		AudioTokenPerSec = 32
		PromptTokenFixed = 350
		OutputTokenEst   = 400
	)

	var totalInput, totalOutput int
	for _, p := range pairs {
		input := ImageTokenCost + int(p.DurationSec*AudioTokenPerSec) + PromptTokenFixed
		output := OutputTokenEst
		totalInput += input
		totalOutput += output
	}

	fmt.Printf("   -> 准备分析 %d 组切片\n", len(pairs))
	fmt.Printf("   -> 输入 token: %d\n", totalInput)
	fmt.Printf("   -> 预计输出 token: %d\n", totalOutput)
	fmt.Print("是否请求 Gemini 分析并生成 Markdown 报告？(y/n): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input != "y" && input != "yes" {
		fmt.Println(">> 已跳过分析")
		return
	}

	// --- 配置加载 ---
	cfg, _ := loadConfig()
	var apiKey, modelName string
	var rpm int = 2

	if cfg != nil {
		apiKey = cfg.GeminiAPIKey
		modelName = cfg.GeminiModelName
		if cfg.GeminiRPM > 0 {
			rpm = cfg.GeminiRPM
		}
	}

	if apiKey == "" {
		fmt.Print("请输入 Gemini API Key: ")
		keyInput, _ := reader.ReadString('\n')
		apiKey = strings.TrimSpace(keyInput)
	}
	if modelName == "" {
		fmt.Print("请输入使用的模型代号 (如 gemini-1.5-pro): ")
		modelInput, _ := reader.ReadString('\n')
		modelName = strings.TrimSpace(modelInput)
	}

	if apiKey == "" || modelName == "" {
		fmt.Println(">> 错误: 缺少 API Key 或模型名称")
		return
	}

	// --- 客户端初始化 ---
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatalf(">> 无法创建 Gemini 客户端: %v", err)
	}
	defer client.Close()

	model := client.GenerativeModel(modelName)
	model.ResponseMIMEType = "application/json"

	// --- 创建报告文件 ---
	videoBaseName := strings.TrimSuffix(originalDataFileName, ".pb.zst")
	timestamp := time.Now().Format("20060102-150405")
	reportFileName := fmt.Sprintf("report_%s_%s.md", videoBaseName, timestamp)

	reportFile, err := os.Create(reportFileName)
	if err != nil {
		log.Fatalf(">> 无法创建报告文件: %v", err)
	}
	defer reportFile.Close()

	header := fmt.Sprintf("# 视频切片分析报告\n\n**视频源**: `%s`\n**时间**: %s\n**模型**: %s\n\n---\n\n",
		videoBaseName, time.Now().Format("2006-01-02 15:04:05"), modelName)
	reportFile.WriteString(header)

	fmt.Printf(">> 开始连接 Gemini (%s)\n", modelName)
	fmt.Printf("   -> RPM 限制: %d 请求/分钟\n", rpm)
	fmt.Printf("   -> 报告文件: %s\n", reportFileName)

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].GroupIndex < pairs[j].GroupIndex
	})

	requestInterval := time.Minute / time.Duration(rpm)
	var lastRequestTime time.Time

	for i, pair := range pairs {
		if !lastRequestTime.IsZero() {
			elapsed := time.Since(lastRequestTime)
			if elapsed < requestInterval {
				waitTime := requestInterval - elapsed
				time.Sleep(waitTime)
			}
		}

		fmt.Printf("   -> [%d/%d] 分析中: %s\n", i+1, len(pairs), pair.ImageName)
		lastRequestTime = time.Now()

		result, err := analyzePair(ctx, model, pair)
		if err != nil {
			fmt.Printf("      ✗ 分析失败: %v\n", err)
			reportFile.WriteString(fmt.Sprintf("## 第 %d 组 (分析失败)\n\n错误信息: %v\n\n---\n\n", pair.GroupIndex, err))
			continue
		}

		fmt.Printf("      ✓ 完成 (相关度: %s)\n", result.CorrelationAnalysis.Percentage)

		mdContent := formatToMarkdown(pair, result)
		if _, err := reportFile.WriteString(mdContent); err != nil {
			fmt.Printf("      ✗ 写入失败: %v\n", err)
		}
	}

	fmt.Printf(">> 分析完成\n")
	fmt.Printf("   -> 报告保存至: %s\n", reportFileName)
}

// analyzePair 调用 Gemini API 分析单个图片和音频文件对。
//
// 该函数会：
//  1. 读取图片和音频文件
//  2. 构建分析提示词
//  3. 调用 Gemini API
//  4. 解析 JSON 响应
//
// 参数：
//   - ctx: 上下文对象
//   - model: Gemini 生成模型
//   - pair: 要分析的文件对
//
// 返回值：
//   - *GeminiResponse: 分析结果
//   - error: API 调用或解析过程中的错误
func analyzePair(ctx context.Context, model *genai.GenerativeModel, pair FilePair) (*GeminiResponse, error) {
	imgData, err := os.ReadFile(pair.ImagePath)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败: %v", err)
	}

	audioData, err := os.ReadFile(pair.AudioPath)
	if err != nil {
		return nil, fmt.Errorf("读取音频失败: %v", err)
	}

	promptText := fmt.Sprintf(
		`分析 %s 和 %s。

请分析语音内容和视觉元素的关联性。

分析维度:
1. 图片内容: 识别画面中的视觉元素、场景、物体、文字等
2. 音频内容: 理解语音所表达的主题、观点、情感
3. 关联分析: 判断视觉内容与语音内容的匹配程度

评分标准:
- 90-100%%: 视觉与音频高度契合，画面直接展示了语音所描述的内容
- 70-89%%: 视觉与音频相关，画面间接支持语音内容
- 50-69%%: 视觉与音频部分相关，存在一定关联但不紧密
- 30-49%%: 视觉与音频关联较弱，画面与语音内容割裂
- 0-29%%: 视觉与音频几乎无关，画面静止或与语音完全无关

请严格输出纯 JSON 格式, 不要包含 Markdown 标记 (如 '''json )。结构如下：
{
    "group_index": "第%d组",
    "image_analysis": { "filename": "%s", "visual_elements": "详细描述画面中的视觉元素、场景、物体、文字等" },
    "audio_analysis": { "filename": "%s", "content": "概括语音所表达的主题、观点、情感" },
    "correlation_analysis": { "description": "详细说明视觉内容与语音内容的匹配程度及原因", "percentage": "关联度百分比(如85%%)" }
}`,
		pair.ImageName, pair.AudioName,
		pair.GroupIndex,
		pair.ImageName, pair.AudioName,
	)

	resp, err := model.GenerateContent(ctx,
		genai.Text(promptText),
		genai.ImageData("jpeg", imgData),
		genai.Blob{MIMEType: "audio/mp3", Data: audioData},
	)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, fmt.Errorf("Gemini 无响应")
	}

	for _, part := range resp.Candidates[0].Content.Parts {
		if txt, ok := part.(genai.Text); ok {
			cleanJSON := strings.TrimSpace(string(txt))
			cleanJSON = strings.TrimPrefix(cleanJSON, "```json")
			cleanJSON = strings.TrimPrefix(cleanJSON, "```")
			cleanJSON = strings.TrimSuffix(cleanJSON, "```")

			var res GeminiResponse
			if err := json.Unmarshal([]byte(cleanJSON), &res); err != nil {
				return nil, fmt.Errorf("JSON 解析失败: %v", err)
			}
			return &res, nil
		}
	}
	return nil, fmt.Errorf("未找到文本响应")
}

// ---------------------------------------------------------
// 4. 工具函数
// ---------------------------------------------------------

// formatTime 将秒数格式化为 HH:MM:SS.ms 格式的时间字符串。
//
// 参数：
//   - seconds: 要格式化的秒数
//
// 返回值：
//   - string: 格式化后的时间字符串，格式为 "HH:MM:SS.ms"
//
// 示例：
//
//	formatTime(3661.25) 返回 "01:01:01.25"
func formatTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := seconds - float64(h*3600+m*60)
	return fmt.Sprintf("%02d:%02d:%05.2f", h, m, s)
}

// generateStaticRanges 根据帧差异计数生成静态片段的时间范围列表。
//
// 该函数遍历每一帧的差异值，当连续多帧的差异值低于阈值时，
// 判定为静态片段。只有持续时间超过最小时长的片段才会被返回。
//
// 参数：
//   - diffCounts: 每一帧的差异计数值切片
//   - threshold: 判定为静态画面的阈值
//   - minDurationSec: 静态片段的最小持续时间（秒）
//   - fps: 视频的帧率
//
// 返回值：
//   - []TimeRange: 符合条件的静态片段时间范围列表
func generateStaticRanges(diffCounts []uint32, threshold, minDurationSec, fps float64) []TimeRange {
	var segments []TimeRange
	inStatic := false
	startFrame := 0

	for i, count := range diffCounts {
		currentFrame := i + 1
		val := float64(count)

		if val < threshold {
			if !inStatic {
				inStatic = true
				startFrame = currentFrame
			}
		} else {
			if inStatic {
				durationFrames := currentFrame - startFrame
				durationSec := float64(durationFrames) / fps
				if durationSec >= minDurationSec {
					segments = append(segments, TimeRange{
						Start: float64(startFrame) / fps,
						End:   float64(currentFrame) / fps,
					})
				}
				inStatic = false
			}
		}
	}
	if inStatic {
		durationFrames := len(diffCounts) - startFrame
		durationSec := float64(durationFrames) / fps
		if durationSec >= minDurationSec {
			segments = append(segments, TimeRange{
				Start: float64(startFrame) / fps,
				End:   float64(len(diffCounts)) / fps,
			})
		}
	}
	return segments
}

// loadAnalysisResult 从 Zstandard 压缩的 Protocol Buffers 文件中加载视频分析结果。
//
// 参数：
//   - path: .pb.zst 文件的路径
//
// 返回值：
//   - *proto.AnalysisResult: 解析后的分析结果
//   - error: 读取、解压或解码过程中的错误
func loadAnalysisResult(path string) (*proto.AnalysisResult, error) {
	compressed, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("无法创建zstd解码器: %v", err)
	}
	defer decoder.Close()

	data, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd解压失败: %v", err)
	}

	var res proto.AnalysisResult
	if err := pb.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("protobuf解码失败: %v", err)
	}
	return &res, nil
}

// ---------------------------------------------------------
// 5. FFmpeg 工具
// ---------------------------------------------------------

// extractFrame 从视频中提取指定时间点的单帧图片。
//
// 使用 FFmpeg 命令行工具提取视频帧。提取的图片质量参数为 2（高质量）。
//
// 参数：
//   - videoPath: 源视频文件路径
//   - outPath: 输出图片文件路径
//   - timeSec: 要提取的时间点（秒）
//
// 返回值：
//   - error: FFmpeg 执行错误，如果成功则返回 nil
//
// 示例：
//
//	err := extractFrame("video.mp4", "frame.jpg", 10.5)
func extractFrame(videoPath, outPath string, timeSec float64) error {
	cmd := exec.Command("ffmpeg", "-y",
		"-ss", fmt.Sprintf("%.3f", timeSec),
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "2",
		outPath,
	)
	return cmd.Run()
}

// extractAudio 从视频中提取指定时间段的音频。
//
// 使用 FFmpeg 命令行工具提取视频中的音频片段，输出为 MP3 格式。
// 音频质量参数为 2（高质量）。
//
// 参数：
//   - videoPath: 源视频文件路径
//   - outPath: 输出音频文件路径
//   - start: 开始时间（秒）
//   - end: 结束时间（秒）
//
// 返回值：
//   - error: FFmpeg 执行错误，如果成功则返回 nil
//
// 示例：
//
//	err := extractAudio("video.mp4", "audio.mp3", 10.0, 30.0)
func extractAudio(videoPath, outPath string, start, end float64) error {
	cmd := exec.Command("ffmpeg", "-y",
		"-ss", fmt.Sprintf("%.3f", start),
		"-to", fmt.Sprintf("%.3f", end),
		"-i", videoPath,
		"-vn",
		"-acodec", "libmp3lame",
		"-q:a", "2",
		outPath,
	)
	return cmd.Run()
}

// formatToMarkdown 将分析结果格式化为 Markdown 格式的字符串。
//
// 生成的 Markdown 包含：
//   - 组号和相关度标题
//   - 图片展示和视觉元素描述
//   - 音频内容描述
//   - 关联分析说明
//
// 参数：
//   - pair: 文件对信息
//   - res: Gemini 分析结果
//
// 返回值：
//   - string: 格式化后的 Markdown 文本
func formatToMarkdown(pair FilePair, res *GeminiResponse) string {
	return fmt.Sprintf(`## %d. 第%d组（**相关度: %s**）
### 图片（%s）
![%s](%s)
%s

### 音频（%s）
%s

### 关联分析
%s

`,
		pair.GroupIndex, pair.GroupIndex,
		res.CorrelationAnalysis.Percentage,
		pair.ImageName,
		pair.ImageName, pair.ImagePath,
		res.ImageAnalysis.VisualElements,
		pair.AudioName,
		res.AudioAnalysis.Content,
		res.CorrelationAnalysis.Description,
	)
}
