package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/gob"
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

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// Config 配置文件结构
type Config struct {
	GeminiAPIKey    string `json:"gemini_api_key"`
	GeminiModelName string `json:"gemini_model_name"`
	GeminiRPM       int    `json:"gemini_rpm"`
}

// loadConfig 从 config.json 加载配置
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

type AnalysisResult struct {
	VideoFile          string
	FPS                float64
	Width              int
	Height             int
	TotalFrames        int
	SuggestedThreshold float64
	DiffCounts         []uint32
}

type TimeRange struct {
	Start float64
	End   float64
}

type FilePair struct {
	GroupIndex    int
	VideoBaseName string
	ImagePath     string
	AudioPath     string
	ImageName     string
	AudioName     string
	DurationSec   float64
}

type GeminiResponse struct {
	GroupIndex    string `json:"group_index"`
	ImageAnalysis struct {
		Filename       string `json:"filename"`
		VisualElements string `json:"visual_elements"`
	} `json:"image_analysis"`
	AudioAnalysis struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	} `json:"audio_analysis"`
	CorrelationAnalysis struct {
		Description string `json:"description"`
		Percentage  string `json:"percentage"`
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
		fmt.Println("   gsp                              # 使用 gob 中保存的推荐阈值")
		fmt.Println("   gsp <threshold>                  # 指定自定义阈值")
		fmt.Println("   gsp <threshold> <min_duration>   # 指定阈值和最小时长")
		fmt.Println()
		fmt.Println(">> 示例:")
		fmt.Println("   gsp           # 使用推荐阈值和默认 20 秒最小时长")
		fmt.Println("   gsp 500       # 使用阈值 500 和默认 20 秒最小时长")
		fmt.Println("   gsp 500 15    # 使用阈值 500 和 15 秒最小时长")
		os.Exit(1)
	}

	var threshold float64 = -1 // -1 表示使用 gob 中的推荐值
	var minDurationSec float64 = 20.0

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
	targetGob := findGobInCurrentDir()
	if targetGob == "" {
		fmt.Println(">> 错误: 当前目录未找到 .gob 文件")
		os.Exit(1)
	}

	// --- 处理单个文件 ---
	pairs, err := processGobFile(targetGob, threshold, minDurationSec)
	if err != nil {
		log.Fatalf(">> 处理文件失败: %v", err)
	}

	// --- Gemini 分析 ---
	if len(pairs) > 0 {
		askAndRunGemini(pairs, targetGob)
	} else {
		fmt.Println(">> 没有生成任何片段, 跳过 Gemini 分析")
	}
}

// findGobInCurrentDir 查找当前目录下第一个 gob 文件 (按字母顺序)
func findGobInCurrentDir() string {
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
		if strings.HasSuffix(strings.ToLower(fileName), ".gob") {
			return fileName
		}
	}
	return ""
}

// processGobFile 返回生成的文件对列表
func processGobFile(gobPath string, threshold, minDuration float64) ([]FilePair, error) {
	var generatedPairs []FilePair

	fmt.Printf(">> 加载分析数据: %s\n", gobPath)

	// A. 读取并解压 Gob
	data, err := loadAnalysisResult(gobPath)
	if err != nil {
		return nil, err
	}

	if threshold < 0 {
		threshold = data.SuggestedThreshold
	}

	fmt.Printf("   -> 视频源: %s\n", filepath.Base(data.VideoFile))
	fmt.Printf("   -> 使用设定值: 阈值 = %.0f 最小时长 = %.0fs\n", threshold, minDuration)

	ranges := generateStaticRanges(data.DiffCounts, threshold, minDuration, data.FPS)

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
	baseNameNoExt := strings.TrimSuffix(filepath.Base(gobPath), ".gob")
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

func askAndRunGemini(pairs []FilePair, originalGobName string) {
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
	videoBaseName := strings.TrimSuffix(originalGobName, ".gob")
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

// analyzePair 调用 Gemini API
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

func formatTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := seconds - float64(h*3600+m*60)
	return fmt.Sprintf("%02d:%02d:%05.2f", h, m, s)
}

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

func loadAnalysisResult(path string) (AnalysisResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return AnalysisResult{}, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("无法解压gzip: %v", err)
	}
	defer gr.Close()

	var res AnalysisResult
	dec := gob.NewDecoder(gr)
	if err := dec.Decode(&res); err != nil {
		return AnalysisResult{}, fmt.Errorf("gob解码失败: %v", err)
	}
	return res, nil
}

// ---------------------------------------------------------
// 5. FFmpeg 工具
// ---------------------------------------------------------

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
