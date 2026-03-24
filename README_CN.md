# GSP - Gemini Static-segment Processor

## Introduction

GSP (Gemini Static-segment Processor) is a video analysis tool that detects static segments in videos and uses Google Gemini AI to analyze the correlation between visual content and audio content.

This tool is primarily used for analyzing scenarios where the video frame is static but has audio commentary. It automatically extracts key frames and corresponding audio, then evaluates content matching through AI.

## Features

- 🎬 **Auto-detect static segments**: Identify static frame regions in videos based on pre-analyzed data
- 🖼️ **Smart content extraction**: Automatically extract representative images and corresponding audio from static segments
- 🤖 **AI content analysis**: Use Google Gemini AI to analyze visual-audio correlation
- 📊 **Generate analysis reports**: Automatically create Markdown reports with relevance scores
- ⚡ **Concurrent processing**: Support multi-threaded concurrent extraction for improved efficiency
- 🎯 **Flexible configuration**: Support custom threshold and minimum duration parameters

## System Requirements

- Go 1.18 or higher
- FFmpeg (for video processing)
- Google Gemini API Key

## Installation

### 1. Install FFmpeg

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
Download and install from [FFmpeg official website](https://ffmpeg.org/download.html)

### 2. Build Program

```bash
go build -o gsp main.go
```

Or use Go install:
```bash
go install
```

## Configuration

Create a `config.json` file in the program directory (optional):

```json
{
  "gemini_api_key": "your-api-key-here",
  "gemini_model_name": "gemini-1.5-pro",
  "gemini_rpm": 2
}
```

Configuration options:
- `gemini_api_key`: Gemini API key (if not configured, you'll be prompted at runtime)
- `gemini_model_name`: Model name to use, recommend `gemini-1.5-pro` or `gemini-1.5-flash`
- `gemini_rpm`: Requests per minute limit, default is 2 (adjust according to your API quota)

## Usage

### Basic Usage

```bash
# Use recommended threshold from .gob file
gsp

# Specify custom threshold
gsp 500

# Specify threshold and minimum duration (seconds)
gsp 500 15
```

### Parameter Description

1. **threshold**: Frame difference threshold, values below this are considered static frames
   - Not specified: Use recommended value from .gob file
   - Smaller value (e.g., 300): More strict, only detects very static frames
   - Larger value (e.g., 800): More lenient, includes slightly changing frames

2. **min_duration**: Minimum duration of static segments (seconds)
   - Default: 20 seconds
   - Recommended: Between 10-30 seconds, too short may generate too many segments

### Complete Workflow

```bash
# 1. Ensure current directory has .gob analysis file and corresponding video file
ls *.gob
ls *.mp4

# 2. Run analysis (using recommended threshold)
gsp

# 3. Program will display detected segment information
# >> Loading analysis data: video_analysis.gob
# >> Video source: example.mp4
# >> Using set values: threshold = 650 min duration = 20s
# >> Found segments: 5
#    [Group] Start time  -  End time      (Duration)
#    [01] 00:01:23.45 - 00:01:58.12 (00:00:34.67)
#    ...

# 4. Confirm whether to perform Gemini analysis
# Request Gemini analysis and generate Markdown report? (y/n): y

# 5. Wait for analysis to complete, view generated report
cat report_*.md
```

## Output Structure

```
project_dir/
├── video_analysis.gob          # Input: Pre-analysis data
├── example.mp4                 # Input: Source video file
├── config.json                 # Configuration file (optional)
├── output_video_analysis/      # Output directory
│   ├── example-1-1.jpg        # Group 1 image
│   ├── example-1-2.mp3        # Group 1 audio
│   ├── example-2-1.jpg        # Group 2 image
│   ├── example-2-2.mp3        # Group 2 audio
│   └── ...
└── report_video_analysis_20241213-143025.md  # Analysis report
```

## Relevance Scoring Criteria

- **90-100%**: Visual and audio highly consistent, frame directly shows what speech describes
- **70-89%**: Visual and audio related, frame indirectly supports audio content
- **50-69%**: Visual and audio partially related, some connection but not tight
- **30-49%**: Visual and audio weakly related, frame disconnected from speech content
- **0-29%**: Visual and audio almost unrelated, frame static or completely unrelated to speech

## Performance Optimization

### Token Usage Estimation

The program displays token usage estimates before analysis:
- Per image: ~258 tokens
- Audio: ~32 tokens/second
- Prompt: ~350 tokens
- Output: ~400 tokens

### Cost Control

1. **Adjust RPM limit**: Set `gemini_rpm` in config.json
2. **Filter segments**: Increase minimum duration parameter to reduce analysis quantity
3. **Choose appropriate model**:
   - `gemini-1.5-flash`: Faster and cheaper, suitable for large batch processing
   - `gemini-1.5-pro`: More accurate, suitable for high-quality analysis

### Concurrent Processing

The program uses up to 4 concurrent threads to extract images and audio, can be modified in code:

```go
sem := make(chan struct{}, 4)  // Modify number to adjust concurrency
```

## License

GPL-3.0

## Contributing

Issues and Pull Requests are welcome!

---

**Note**: Before using this tool, ensure you have the right to process relevant video content and comply with Google Gemini API terms of service.
