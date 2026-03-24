# GSP - Gemini Static-segment Processor

A video analysis tool that detects static segments in videos and uses Google Gemini AI to analyze the correlation between visual content and audio content.

## Features

- **Auto-detect static segments**: Identify static frame regions based on pre-analyzed data
- **Smart content extraction**: Extract representative images and audio from static segments
- **AI content analysis**: Use Google Gemini AI for visual-audio correlation analysis
- **Generate analysis reports**: Create Markdown reports with relevance scores
- **Concurrent processing**: Multi-threaded extraction for improved efficiency

## System Requirements

- Go 1.18+
- FFmpeg
- Google Gemini API Key

## Installation

```bash
go build -o gsp main.go
```

## Usage

```bash
# Use recommended threshold from data file
gsp

# Specify custom threshold
gsp 500

# Specify threshold and minimum duration (seconds)
gsp 500 15
```

## Configuration

Create `config.json` (optional):

```json
{
  "gemini_api_key": "your-api-key-here",
  "gemini_model_name": "gemini-1.5-pro",
  "gemini_rpm": 2
}
```

## Output Structure

```
project_dir/
├── video_analysis.gob          # Input: Pre-analysis data
├── example.mp4                 # Input: Source video
├── output_video_analysis/      # Output directory
│   ├── example-1-1.jpg        # Group 1 image
│   ├── example-1-2.mp3        # Group 1 audio
│   └── ...
└── report_*.md                # Analysis report
```

## License

GPL-3.0
