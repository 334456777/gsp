# CLAUDE.md

Guidance for Claude Code working with this repository.

## Project Overview

GSP (Gemini Static-segment Processor) analyzes static video segments using Google Gemini AI to evaluate visual-audio correlation. Reads frame difference data from `.pb.zst` files, detects static segments, extracts images/audio, and evaluates content matching.

## Build and Run

```bash
make build              # Build binary
./gsp                   # Use recommended threshold
./gsp 500              # Custom threshold
./gsp 500 15           # Threshold + min duration
make clean             # Delete binary
```

## Code Architecture

**Data Flow:**
1. Load `AnalysisResult` protobuf from `.pb.zst`
2. Detect static segments via `generateStaticRanges()`
3. Extract images/audio with FFmpeg (4 concurrent threads)
4. Analyze with Gemini AI via `askAndRunGemini()`
5. Generate Markdown report via `formatToMarkdown()`

**Key Structures:**
- `AnalysisResult`: Video analysis from `.pb.zst`
- `TimeRange`: Video time period (Start/End in seconds)
- `FilePair`: Image/audio file pair with metadata
- `GeminiResponse`: Structured AI analysis response

**Configuration:**
- Read from `config.json`: API key, model name, RPM limit
- Prompt for API key at runtime if not provided

**FFmpeg Integration:**
- Extract frame: `ffmpeg -i <video> -ss <time> -vframes 1 <output>.jpg`
- Extract audio: `ffmpeg -i <video> -ss <start> -to <end> <output>.mp3`

**Dependencies:**
- `github.com/google/generative-ai-go/genai`
- `github.com/klauspost/compress/zstd`
- `google.golang.org/protobuf`

**File Naming:**
- Input: `<basename>.pb.zst`
- Output: `output_<basename>/` with `<basename>-<group>-1.jpg` and `<basename>-<group>-2.mp3`
- Report: `report_<basename>_<timestamp>.md`
