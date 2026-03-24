# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GSP (Gemini Static-segment Processor) is a video static segment analysis tool that uses Google Gemini AI to analyze the correlation between visual content and audio content in static video frames. The tool reads frame difference data from pre-analyzed `.pb.zst` files, detects static segments, extracts images and audio, then evaluates content matching through Gemini AI.

## Build and Run

### Build Commands

```bash
# Build using Makefile (recommended)
make build

# Or use go build directly
go build -o gsp main.go

# Install to system path (requires sudo)
make install
```

### Run Program

```bash
# Use recommended threshold from data file
./gsp

# Specify custom threshold
./gsp 500

# Specify threshold and minimum duration (seconds)
./gsp 500 15
```

### Clean

```bash
make clean    # Delete built binary
```

## Code Architecture

### Core Data Flow

1. **Input Phase**: Load `AnalysisResult` protobuf data from `.pb.zst` file
   - Data structure definitions: `proto/analysis.proto` and `proto/analysis.pb.go`
   - Contains frame difference array (`diff_counts`), suggested threshold (`suggested_threshold`), FPS, etc.

2. **Detection Phase**: `generateStaticRanges()` detects static segments based on threshold and minimum duration
   - Iterates through `diff_counts` array, consecutive frames below threshold form static segments
   - Returns `[]TimeRange` list of time ranges

3. **Extraction Phase**: Concurrently call FFmpeg to extract images and audio
   - `extractFrame()`: Extract middle frame of static segment as representative image
   - `extractAudio()`: Extract complete audio segment
   - Up to 4 concurrent threads (controlled by semaphore)

4. **Analysis Phase**: `askAndRunGemini()` calls Gemini AI for multimodal analysis
   - Send each segment's image and audio together to Gemini
   - Request structured JSON response (`GeminiResponse`)
   - Includes visual element analysis, audio content analysis, relevance percentage

5. **Report Phase**: `formatToMarkdown()` generates Markdown report

### Key Data Structures

- **`AnalysisResult`** (protobuf): Video analysis results loaded from `.pb.zst`
- **`TimeRange`**: Represents video time period (Start/End in seconds)
- **`FilePair`**: Extracted image and audio file pair, including GroupIndex, paths, duration, etc.
- **`GeminiResponse`**: Structured response from AI analysis (image analysis, audio analysis, correlation)

### Configuration Management

Read configuration from `config.json` (optional):
- `gemini_api_key`: Gemini API key
- `gemini_model_name`: Model name (default "gemini-1.5-pro")
- `gemini_rpm`: Requests per minute limit (default 2)

If API key is not provided, user will be prompted at runtime.

### FFmpeg Integration

Call FFmpeg through `os/exec.Command`:
- Extract frame: `ffmpeg -i <video> -ss <time> -vframes 1 <output>.jpg`
- Extract audio: `ffmpeg -i <video> -ss <start> -to <end> <output>.mp3`

### Concurrent Processing

Extraction phase uses semaphore pattern to control concurrency:
```go
sem := make(chan struct{}, 4)  // Max 4 concurrent
```

## Development Notes

### Protobuf Generation

To modify `proto/analysis.proto`, regenerate Go code:
```bash
protoc --go_out=. proto/analysis.proto
```

### Dependency Management

Main dependencies:
- `github.com/google/generative-ai-go/genai`: Gemini AI SDK
- `github.com/klauspost/compress/zstd`: Zstandard decompression
- `google.golang.org/protobuf`: Protocol Buffers

### File Naming Conventions

- Input: `<basename>.pb.zst` (Pre-analyzed video data)
- Output directory: `output_<basename>/`
- Output files: `<basename>-<group>-1.jpg` (image), `<basename>-<group>-2.mp3` (audio)
- Report: `report_<basename>_<timestamp>.md`

### Error Handling

The program uses `log.Fatal` to exit on critical errors (e.g., data file not found, FFmpeg failure). When adding new features, maintain consistent error handling patterns.
