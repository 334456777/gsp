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
	"strconv"
	"strings"
	"sync"
	"time"
	"sort"

	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
)

// ---------------------------------------------------------
// 1. 数据结构
// ---------------------------------------------------------

type AnalysisResult struct {
	VideoFile    string
	AnalysisTime string
	FPS          float64
	Width        int
	Height       int
	TotalFrames  int
	Threshold    float64
	CropHeight   int
	DiffCounts   []int32
}

type TimeRange struct {
	Start float64
	End   float64
}

// FilePair 存储生成的一对文件路径, 用于后续发给 Gemini
type FilePair struct {
	GroupIndex    int
	VideoBaseName string
	ImagePath     string
	AudioPath     string
	ImageName     string
	AudioName     string
	DurationSec   float64
}

// GeminiResponse 用于解析 Gemini 返回的 JSON 结构
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
	// --- 参数解析区域 ---
	args := os.Args[1:]

	// 默认值
	var threshold float64
	var minDurationSec float64 = 20.0 // 默认 20 秒

	// 打印使用帮助并退出的函数
	printUsageAndExit := func() {
		fmt.Println("Usage: gsp <threshold> [minDurationSec]")
		fmt.Println("注意: Gemini 分析功能将优先读取 .env 文件中的 GEMINI_API_KEY。")
		os.Exit(1)
	}

	// 逻辑：
	// 1. 无参数 -> 提示
	// 2. 参数数量 > 2 -> 提示
	if len(args) == 0 || len(args) > 2 {
		printUsageAndExit()
	}

	// 解析第一个参数 (Threshold - 必需)
	if val, err := strconv.ParseFloat(args[0], 64); err == nil && val > 0 {
		threshold = val
	} else {
		fmt.Printf("错误: 阈值参数不合法: %s\n", args[0])
		printUsageAndExit()
	}

	// 解析第二个参数 (MinDurationSec - 可选)
	if len(args) == 2 {
		if val, err := strconv.ParseFloat(args[1], 64); err == nil && val > 0 {
			minDurationSec = val
		} else {
			fmt.Printf("错误: 时长参数不合法: %s\n", args[1])
			printUsageAndExit()
		}
	}
	// --------------------

	files, err := filepath.Glob("*.gob")
	if err != nil {
		log.Fatalf("查找文件失败: %v", err)
	}

	if len(files) == 0 {
		fmt.Println("当前目录下未找到 .gob 文件。")
		return
	}

	if len(files) > 1 {
		fmt.Printf("发现 %d 个任务 (设定阈值: %.0f, 最小静止时长: %.1fs)...\n", len(files), threshold, minDurationSec)
	}

	// 收集所有生成的文件对
	var allGeneratedPairs []FilePair

	for _, gobFile := range files {
		pairs, err := processGobFile(gobFile, threshold, minDurationSec)
		if err != nil {
			log.Printf("处理文件 [ %s ] 失败: %v\n", gobFile, err)
		} else {
			allGeneratedPairs = append(allGeneratedPairs, pairs...)
		}
	}

	if len(files) > 1 {
		fmt.Println("所有文件提取任务处理完毕。")
	}

	// ---------------------------------------------------------
	// 新增：Gemini 分析询问环节
	// ---------------------------------------------------------
	if len(allGeneratedPairs) > 0 {
		askAndRunGemini(allGeneratedPairs)
	} else {
		fmt.Println("没有生成任何片段, 跳过 Gemini 分析。")
	}
}

// processGobFile 返回生成的文件对列表
func processGobFile(gobPath string, threshold, minDuration float64) ([]FilePair, error) {
	fmt.Printf("正在读取: %s\n", gobPath)

	var generatedPairs []FilePair

	// A. 读取并解压 Gob
	data, err := loadAnalysisResult(gobPath)
	if err != nil {
		return nil, err
	}

	fmt.Printf("  -> 视频源: %s\n", filepath.Base(data.VideoFile))
	ranges := generateStaticRanges(data.DiffCounts, threshold, minDuration, data.FPS)
	fmt.Printf("  -> 使用设定值: %.0f %.1f\n", threshold, minDuration)

	if len(ranges) == 0 {
		fmt.Printf("  -> ⚠️  未发现 %.1fs 以上的静止片段 (阈值: %.0f)\n", minDuration, threshold)
		return nil, nil
	}

	// --- 打印详细时间表 ---
	fmt.Printf("  -> 符合条件的片段: %d 个\n", len(ranges))
	fmt.Println("     [组号] 开始时间  -  结束时间      (时长)")
	for i, r := range ranges {
		startStr := formatTime(r.Start)
		endStr := formatTime(r.End)
		durStr := formatTime(r.End - r.Start)
		fmt.Printf("     [%02d] %s - %s (%s)\n", i+1, startStr, endStr, durStr)
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

	// E. 并发执行 FFmpeg
	var wg sync.WaitGroup
	var mu sync.Mutex // 保护 generatedPairs
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

			// 修改：检查图片是否存在
			if _, err := os.Stat(imgPath); err == nil {
				// fmt.Printf("  [Info] 跳过已存在的图片: %s\n", imgName)
			} else {
				_ = extractFrame(videoPath, imgPath, snapTime)
			}

			// 2. 音频处理
			audioName := fmt.Sprintf("%s-%d-2.mp3", videoBaseName, idx)
			audioPath := filepath.Join(outputDir, audioName)

			// 修改：检查音频是否存在
			if _, err := os.Stat(audioPath); err == nil {
				// fmt.Printf("  [Info] 跳过已存在的音频: %s\n", audioName)
			} else {
				_ = extractAudio(videoPath, audioPath, tr.Start, tr.End)
			}

			// 3. 记录生成的文件对
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
	fmt.Printf("  -> 完成, 输出至: %s/\n", outputDir)
	return generatedPairs, nil
}

// ---------------------------------------------------------
// 3. Gemini 分析模块 (支持 .env)
// ---------------------------------------------------------

func askAndRunGemini(pairs []FilePair) {
	fmt.Println("---------------------------------------------------------")

	// --- Token 计算逻辑 ---
	const (
		ImageTokenCost   = 258 // 每张图片固定消耗
		AudioTokenPerSec = 32  // 每秒音频消耗
		PromptTokenFixed = 350 // 提示词文本固定消耗 (预估)
		OutputTokenEst   = 400 // 每次请求预估输出
	)

	var totalInput int
	var totalOutput int

	for _, p := range pairs {
		// 单次输入 = 图片 + 音频(秒*32) + 文本
		input := ImageTokenCost + int(p.DurationSec*AudioTokenPerSec) + PromptTokenFixed
		// 单次输出 = 预估值
		output := OutputTokenEst

		totalInput += input
		totalOutput += output
	}

	// 打印预估
	fmt.Printf("  -> 输入token: %d\n", totalInput)
	fmt.Printf("  -> 预计输出token: %d\n", totalOutput)
	fmt.Println("---------------------------------------------------------")
	fmt.Print("是否请求 Gemini 分析并生成 Markdown 报告？(y/n): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input != "y" && input != "yes" {
		fmt.Println("已跳过分析")
		return
	}

	// 1. 尝试加载 .env 文件
	err := godotenv.Load()

	// 2. 获取 API Key
	apiKey := os.Getenv("GEMINI_API_KEY")
	modelName := os.Getenv("GEMINI_MODEL_NAME")

	if apiKey == "" {
		fmt.Print("请输入Gemini API Key: ")
		keyInput, _ := reader.ReadString('\n')
		apiKey = strings.TrimSpace(keyInput)
		if apiKey == "" {
			fmt.Println("错误: API Key 不能为空")
			return
		}
	}
	if modelName == "" {
		fmt.Print("请输入使用的模型代号: ")
		modelInput, _ := reader.ReadString('\n')
		modelName = strings.TrimSpace(modelInput)
		if modelName == "" {
			fmt.Println("错误: 模型代号不能为空")
			return
		}
	}

	// 初始化客户端
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatalf("无法创建 Gemini 客户端: %v", err)
	}
	defer client.Close()

	model := client.GenerativeModel(modelName)

	// 强制输出 JSON
	model.ResponseMIMEType = "application/json"

	// --- 创建 Markdown 报告文件 ---
	timestamp := time.Now().Format("20060102-150405")
	reportFileName := fmt.Sprintf("report_%s.md", timestamp)
	reportFile, err := os.Create(reportFileName)
	if err != nil {
		log.Fatalf("无法创建报告文件: %v", err)
	}
	defer reportFile.Close()

	// 写入文件头
	header := fmt.Sprintf("# 视频切片分析报告\n\n生成时间: %s 使用模型: %s\n\n---\n\n", time.Now().Format("2006-01-02 15:04:05"), modelName)
	reportFile.WriteString(header)

	fmt.Printf("  -> 开始连接 Gemini (%s) \n", modelName)
	fmt.Printf("  -> 结果将写入: %s\n", reportFileName)

	sort.Slice(pairs, func(i, j int) bool {
        return pairs[i].GroupIndex < pairs[j].GroupIndex
    })

	for i, pair := range pairs {
		fmt.Printf("     [%d/%d] 分析中: %s ... \n", i+1, len(pairs), pair.ImageName)
		result, err := analyzePair(ctx, model, pair)
		if err != nil {
			fmt.Printf("  -> 分析失败: %v\n", err)
			reportFile.WriteString(fmt.Sprintf("## 第 %d 组 (分析失败)\n\n错误信息: %v\n\n---\n\n", pair.GroupIndex, err))
			continue
		}

		mdContent := formatToMarkdown(pair, result)
		if _, err := reportFile.WriteString(mdContent); err != nil {
			fmt.Printf("写入文件失败: %v\n", err)
		} else {
			// fmt.Println("完成")
		}
	}
	fmt.Printf("  -> 分析报告: %s\n", reportFileName)
}

func analyzePair(ctx context.Context, model *genai.GenerativeModel, pair FilePair) (*GeminiResponse, error) {
	imgData, err := os.ReadFile(pair.ImagePath)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败: %v", err)
	}

	audioData, err := os.ReadFile(pair.AudioPath)
	if err != nil {
		return nil, fmt.Errorf("读取音频失败: %v", err)
	}

	// 提示词：强制 JSON 输出
	promptText := fmt.Sprintf(
		`分析 %s 和 %s。
        请分析语音内容和视觉元素的关联性。
        请严格输出纯 JSON 格式, 不要包含 Markdown 标记 (如 '''json )。结构如下：
        {
            "group_index": "第%d组",
            "image_analysis": { "filename": "%s", "visual_elements": "图片内容描述" },
            "audio_analysis": { "filename": "%s", "content": "音频内容概括" },
            "correlation_analysis": { "description": "关联性分析结论", "percentage": "关联度百分比" }
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
			// 清理可能存在的 Markdown 代码块标记
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
// 4. 核心算法 & 工具函数
// ---------------------------------------------------------

func formatTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := seconds - float64(h*3600+m*60)
	return fmt.Sprintf("%02d:%02d:%05.2f", h, m, s)
}

func generateStaticRanges(diffCounts []int32, threshold, minDurationSec, fps float64) []TimeRange {
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
	err = dec.Decode(&res)
	return res, err
}

// ---------------------------------------------------------
// 5. FFmpeg 调用
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
	// 相对路径引用图片
	relImgPath := pair.ImagePath

	// 按照你要求的格式构建字符串
	return fmt.Sprintf(`## %d. 第%d组（**相关度: %s**）
### 图片（%s）
![%s](%s)
%s

### 音频（%s）
%s

### 关联分析
%s

`,
		pair.GroupIndex, pair.GroupIndex, // 对应：## 6. 第6组
		res.CorrelationAnalysis.Percentage, // 对应：**相关度: 95%**
		pair.ImageName,                     // 对应：### 图片 (文件名):
		pair.ImageName, relImgPath,         // (我保留了图片显示, 否则报告里看不到图)
		res.ImageAnalysis.VisualElements,    // 对应：视觉元素: ...
		pair.AudioName,                      // 对应：### 音频 (文件名):
		res.AudioAnalysis.Content,           // 对应：语音内容: ...
		res.CorrelationAnalysis.Description, // 对应：关联分析: ...
	)
}
