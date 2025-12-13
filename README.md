# 视频静态片段分析工具 (GSP)

## 项目简介

GSP (Gemini Static-segment Processor) 是一个视频分析工具，用于检测视频中的静态片段，并使用 Google Gemini AI 分析视觉内容与音频内容的相关性。

该工具主要用于分析视频中画面静止但有音频解说的场景，自动提取关键帧和对应音频，并通过 AI 评估内容的匹配度。

## 功能特性

- 🎬 **自动检测静态片段**：基于预分析数据识别视频中的静止画面区域
- 🖼️ **智能提取内容**：自动提取静态片段的代表性图片和对应音频
- 🤖 **AI 内容分析**：使用 Google Gemini AI 分析视觉与音频的相关性
- 📊 **生成分析报告**：自动生成包含相关度评分的 Markdown 报告
- ⚡ **并发处理**：支持多线程并发提取，提高处理效率
- 🎯 **灵活配置**：支持自定义阈值和最小时长参数

## 系统要求

- Go 1.18 或更高版本
- FFmpeg（用于视频处理）
- Google Gemini API Key

## 安装

### 1. 安装 FFmpeg

**macOS:**
```bash
brew install ffmpeg
```

**Ubuntu/Debian:**
```bash
sudo apt update
sudo apt install ffmpeg
```

**Windows:**
从 [FFmpeg 官网](https://ffmpeg.org/download.html) 下载并安装

### 2. 编译程序

```bash
go build -o gsp main.go
```

或者使用 Go install：
```bash
go install
```

## 配置

在程序运行目录创建 `config.json` 文件（可选）：

```json
{
  "gemini_api_key": "your-api-key-here",
  "gemini_model_name": "gemini-1.5-pro",
  "gemini_rpm": 2
}
```

配置项说明：
- `gemini_api_key`: Gemini API 密钥（如未配置，运行时会提示输入）
- `gemini_model_name`: 使用的模型名称，推荐 `gemini-1.5-pro` 或 `gemini-1.5-flash`
- `gemini_rpm`: 每分钟请求限制，默认为 2（根据你的 API 配额调整）

## 使用方法

### 基本用法

```bash
# 使用 .gob 文件中的推荐阈值
gsp

# 指定自定义阈值
gsp 500

# 指定阈值和最小时长（秒）
gsp 500 15
```

### 参数说明

1. **threshold（阈值）**：帧差异值的阈值，低于此值被认为是静态画面
   - 不指定：使用 .gob 文件中的推荐值
   - 较小值（如 300）：更严格，只检测非常静止的画面
   - 较大值（如 800）：更宽松，包含轻微变化的画面

2. **min_duration（最小时长）**：静态片段的最小持续时间（秒）
   - 默认：20 秒
   - 建议：10-30 秒之间，太短可能产生过多片段

### 完整工作流程

```bash
# 1. 确保当前目录有 .gob 分析文件和对应的视频文件
ls *.gob
ls *.mp4

# 2. 运行分析（使用推荐阈值）
gsp

# 3. 程序会显示检测到的片段信息
# >> 加载分析数据: video_analysis.gob
# >> 视频源: example.mp4
# >> 使用设定值: 阈值 = 650 最小时长 = 20s
# >> 符合条件的片段: 5 个
#    [组号] 开始时间  -  结束时间      (时长)
#    [01] 00:01:23.45 - 00:01:58.12 (00:00:34.67)
#    ...

# 4. 确认是否进行 Gemini 分析
# 是否请求 Gemini 分析并生成 Markdown 报告？(y/n): y

# 5. 等待分析完成，查看生成的报告
cat report_*.md
```

## 输出结构

```
project_dir/
├── video_analysis.gob          # 输入：预分析数据
├── example.mp4                 # 输入：源视频文件
├── config.json                 # 配置文件（可选）
├── output_video_analysis/      # 输出目录
│   ├── example-1-1.jpg        # 第1组图片
│   ├── example-1-2.mp3        # 第1组音频
│   ├── example-2-1.jpg        # 第2组图片
│   ├── example-2-2.mp3        # 第2组音频
│   └── ...
└── report_video_analysis_20241213-143025.md  # 分析报告
```

## 分析报告示例

生成的 Markdown 报告包含：

```markdown
# 视频切片分析报告

**视频源**: `example`
**时间**: 2024-12-13 14:30:25
**模型**: gemini-1.5-pro

---

## 1. 第1组（**相关度: 85%**）
### 图片（example-1-1.jpg）
![example-1-1.jpg](output_example/example-1-1.jpg)
画面显示一个产品展示界面，包含产品图片、价格信息和功能特点列表...

### 音频（example-1-2.mp3）
讲解者详细介绍了产品的核心功能和使用场景...

### 关联分析
视觉内容与音频内容高度契合。画面中展示的产品特点与语音解说完全对应...
```

## 相关度评分标准

- **90-100%**：视觉与音频高度契合，画面直接展示了语音所描述的内容
- **70-89%**：视觉与音频相关，画面间接支持语音内容
- **50-69%**：视觉与音频部分相关，存在一定关联但不紧密
- **30-49%**：视觉与音频关联较弱，画面与语音内容割裂
- **0-29%**：视觉与音频几乎无关，画面静止或与语音完全无关

## 性能优化建议

### Token 使用估算

程序会在分析前显示 Token 使用预估：
- 每张图片：约 258 tokens
- 音频：约 32 tokens/秒
- 提示词：约 350 tokens
- 输出：约 400 tokens

### 成本控制

1. **调整 RPM 限制**：在 config.json 中设置 `gemini_rpm`
2. **筛选片段**：提高最小时长参数，减少分析数量
3. **选择合适模型**：
   - `gemini-1.5-flash`：更快更便宜，适合大批量处理
   - `gemini-1.5-pro`：更准确，适合高质量分析

### 并发处理

程序使用最多 4 个并发线程提取图片和音频，可以在代码中修改：

```go
sem := make(chan struct{}, 4)  // 修改数字调整并发数
```

## 常见问题

### Q: 找不到 .gob 文件
**A:** 确保当前目录有预分析生成的 .gob 文件。这个文件需要由视频分析工具预先生成。

### Q: 找不到视频文件
**A:** 程序会先在 .gob 文件记录的路径查找视频，如果找不到会在当前目录查找同名文件。确保视频文件与 .gob 文件在同一目录。

### Q: FFmpeg 命令失败
**A:** 检查 FFmpeg 是否正确安装：`ffmpeg -version`

### Q: Gemini API 报错
**A:** 检查：
- API Key 是否正确
- 是否超出配额限制
- 网络连接是否正常
- 模型名称是否正确

### Q: 没有检测到任何片段
**A:** 尝试：
- 降低阈值参数（如从 650 降到 400）
- 减少最小时长（如从 20 秒降到 10 秒）
- 检查 .gob 文件是否有效

## 技术架构

### 核心模块

1. **数据加载模块**：读取和解压 gzip 压缩的 gob 文件
2. **片段检测模块**：基于帧差异值检测静态区域
3. **内容提取模块**：并发调用 FFmpeg 提取图片和音频
4. **AI 分析模块**：调用 Gemini API 进行多模态内容分析
5. **报告生成模块**：格式化输出 Markdown 报告

### 依赖库

```go
import (
    "github.com/google/generative-ai-go/genai"  // Gemini AI SDK
    "google.golang.org/api/option"              // Google API 选项
)
```

## 开发指南

### 项目结构

```
main.go
├── 数据结构定义
│   ├── Config          # 配置
│   ├── AnalysisResult  # 分析结果
│   ├── TimeRange       # 时间范围
│   ├── FilePair        # 文件对
│   └── GeminiResponse  # AI 响应
├── 主程序流程
│   └── main()
├── Gemini 分析模块
│   ├── askAndRunGemini()
│   └── analyzePair()
├── 工具函数
│   ├── formatTime()
│   ├── generateStaticRanges()
│   └── loadAnalysisResult()
└── FFmpeg 工具
    ├── extractFrame()
    ├── extractAudio()
    └── formatToMarkdown()
```

### 扩展开发

**添加新的分析维度：**
修改 Gemini prompt 和 GeminiResponse 结构

**支持其他 AI 模型：**
修改 analyzePair() 函数的 API 调用逻辑

**自定义输出格式：**
修改 formatToMarkdown() 函数

## 许可证

GPL-3.0

## 贡献

欢迎提交 Issue 和 Pull Request！

---

**注意**：使用本工具前请确保您有权处理相关视频内容，并遵守 Google Gemini API 的使用条款。
