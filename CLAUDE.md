# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

GSP (Gemini Static-segment Processor) 是一个视频静态片段分析工具，使用 Google Gemini AI 分析视频中静止画面的视觉与音频内容相关性。工具从预分析的 `.pb.zst` 文件读取帧差异数据，检测静态片段，提取图片和音频，然后通过 Gemini AI 评估内容匹配度。

## 构建和运行

### 构建命令

```bash
# 使用 Makefile 构建（推荐）
make build

# 或者直接使用 go build
go build -o gsp main.go

# 安装到系统路径（需要 sudo）
make install
```

### 运行程序

```bash
# 使用数据文件中的推荐阈值
./gsp

# 指定自定义阈值
./gsp 500

# 指定阈值和最小时长（秒）
./gsp 500 15
```

### 清理

```bash
make clean    # 删除构建的二进制文件
```

## 代码架构

### 核心数据流

1. **输入阶段**：从 `.pb.zst` 文件加载 `AnalysisResult` protobuf 数据
   - 数据结构定义：`proto/analysis.proto` 和 `proto/analysis.pb.go`
   - 包含帧差异数组（`diff_counts`）、建议阈值（`suggested_threshold`）、FPS 等

2. **检测阶段**：`generateStaticRanges()` 根据阈值和最小时长检测静态片段
   - 遍历 `diff_counts` 数组，值低于阈值的连续帧构成静态片段
   - 返回 `[]TimeRange` 时间范围列表

3. **提取阶段**：并发调用 FFmpeg 提取图片和音频
   - `extractFrame()`：提取静态片段中间帧作为代表性图片
   - `extractAudio()`：提取完整音频片段
   - 最多 4 个并发线程（通过信号量控制）

4. **分析阶段**：`askAndRunGemini()` 调用 Gemini AI 进行多模态分析
   - 每个片段的图片和音频一起发送给 Gemini
   - 请求 JSON 格式的结构化响应（`GeminiResponse`）
   - 包含视觉元素分析、音频内容分析、相关度百分比

5. **报告阶段**：`formatToMarkdown()` 生成 Markdown 报告

### 关键数据结构

- **`AnalysisResult`** (protobuf)：从 `.pb.zst` 加载的视频分析结果
- **`TimeRange`**：表示视频时间段（Start/End 以秒为单位）
- **`FilePair`**：提取的图片和音频文件对，包含 GroupIndex、路径、时长等
- **`GeminiResponse`**：AI 分析的结构化响应（图片分析、音频分析、相关度）

### 配置管理

从 `config.json` 读取配置（可选）：
- `gemini_api_key`：Gemini API 密钥
- `gemini_model_name`：模型名称（默认 "gemini-1.5-pro"）
- `gemini_rpm`：每分钟请求限制（默认 2）

如果未提供 API key，运行时会提示输入。

### FFmpeg 集成

通过 `os/exec.Command` 调用 FFmpeg：
- 提取帧：`ffmpeg -i <video> -ss <time> -vframes 1 <output>.jpg`
- 提取音频：`ffmpeg -i <video> -ss <start> -to <end> <output>.mp3`

### 并发处理

提取阶段使用信号量模式控制并发数：
```go
sem := make(chan struct{}, 4)  // 最多 4 个并发
```

## 开发注意事项

### Protobuf 生成

如需修改 `proto/analysis.proto`，重新生成 Go 代码：
```bash
protoc --go_out=. proto/analysis.proto
```

### 依赖管理

主要依赖：
- `github.com/google/generative-ai-go/genai`：Gemini AI SDK
- `github.com/klauspost/compress/zstd`：Zstandard 解压缩
- `google.golang.org/protobuf`：Protocol Buffers

### 文件命名约定

- 输入：`<basename>.pb.zst`（视频预分析数据）
- 输出目录：`output_<basename>/`
- 输出文件：`<basename>-<group>-1.jpg`（图片）、`<basename>-<group>-2.mp3`（音频）
- 报告：`report_<basename>_<timestamp>.md`

### 错误处理

程序使用 `log.Fatal` 在关键错误时退出（如找不到数据文件、FFmpeg 失败）。在添加新功能时，保持一致的错误处理模式。
