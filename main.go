package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// Decoding Movie data into JSON
type VideoFileInfoProbe struct {
	Streams []struct {
		Index       int    `json:"index"`
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Channels    int    `json:"channels"`
		Bitrate     string `json:"bit_rate"`
		Disposition struct {
			Default int `json:"default"`
		} `json:"disposition"`
		Tags struct {
			Language string `json:"language"`
			// HandlerName string `json:"handler_name"`
		} `json:"tags"`
	} `json:"streams"`
}

func main() {
	fileFlag := flag.String("f", "", "a string")
	directoryFlag := flag.String("d", "", "a string")
	recursiveFlag := flag.Bool("r", false, "a bool")

	flag.Parse()

	filePath := *fileFlag
	directoryPath := *directoryFlag
	isRecursive := *recursiveFlag

	// Check that one flag is being used.
	if len(filePath) == 0 && len(directoryPath) == 0 {
		panic("Please use one flag. Either -f for a single file conversion or -d for a directory to convert files in batch.")
	}
	if len(filePath) > 0 && len(directoryPath) > 0 {
		panic("Cannot use -f and -d flags at the same time.")
	}

	// Convert single file.
	if len(filePath) > 0 {
		fmt.Println("Obtainig file information")
		data, err := ffmpeg.Probe(filePath)
		if err != nil {
			panic(err)
		}

		err = convert(data, filePath)
		if err != nil {
			fmt.Println(err)
		}
	}

	if len(directoryPath) > 0 && !isRecursive {
		fmt.Println("Scanning files inside directory.")

		// Convert all video files in the directory path.
		files, err := fileMatchDir(directoryPath)
		if err != nil {
			panic(err)
		}

		for _, filePath := range files {
			data, err := ffmpeg.Probe(filePath)
			if err != nil {
				panic(err)
			}

			err = convert(data, filePath)
			if err != nil {
				fmt.Println(err)
			}
		}
	}

	if len(directoryPath) > 0 && isRecursive {
		fmt.Println("Scanning directory and subdirectories for video files.")

		// Convert video files inside directory recursively.
		files, err := walkMatch(directoryPath)
		if err != nil {
			panic(err)
		}

		for _, filePath := range files {
			data, err := ffmpeg.Probe(filePath)
			if err != nil {
				panic(err)
			}

			err = convert(data, filePath)
			if err != nil {
				fmt.Println(err)
			}
		}

	}
}

func fileMatchDir(root string) ([]string, error) {
	var matches []string

	files, err := ioutil.ReadDir(root)
	if err != nil {
		panic(err)
	}

	for _, file := range files {
		if !file.IsDir() {
			switch filepath.Ext(file.Name()) {
			case ".mp4", ".mkv", ".mov":
				matches = append(matches, file.Name())
			}
		}
	}

	return matches, nil
}

func walkMatch(root string) ([]string, error) {
	var matches []string

	err := filepath.WalkDir(root, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".mp4", ".mkv", ".mov":
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return matches, nil
}

func convert(fileData string, filePath string) error {

	var (
		totalAudioStreams       int
		audioStreamIndex        int
		audioDefaultStreamIndex int
		process                 string
		audioBitrate            string
	)

	var vFileInfo VideoFileInfoProbe
	err := json.Unmarshal([]byte(fileData), &vFileInfo)
	if err != nil {
		panic(err)
	}

	for _, s := range vFileInfo.Streams {

		// Check that file codec is not h265. h265 transcoding to h264 is not supported yet :).
		if s.CodecType == "video" && s.CodecName == "h265" || s.CodecName == "hevc" {
			err := fmt.Sprintf("%v encoding is not supported\n", s.CodecName)
			return errors.New(err)
		}

		if s.CodecType == "audio" && s.CodecName == "aac" && s.Channels == 2 && s.Disposition.Default == 1 {
			fmt.Println("File meets requirements. Remuxing not needed")
			return nil
		}

		// Extract subs
		subStreamIndex := 0
		if s.CodecType == "subtitle" {
			customNamingTag := ""
			// if s.Tags.HandlerName == "Hearing Impaired" {
			// 	customNamingTag = ".hi"
			// }
			switch s.Tags.Language {
			case "eng", "en", "spa", "es":
				extractSubs(s.Tags.Language, filePath, subStreamIndex, customNamingTag, filePath)
			}

			subStreamIndex++
		}

		// Add to amount of audio streams
		if s.CodecType == "audio" {
			totalAudioStreams++
		}

		// Check if there is an audio AAC 2.0 stream that is not default
		if s.CodecType == "audio" && s.CodecName == "aac" && s.Channels == 2 {
			audioStreamIndex = totalAudioStreams - 1
			process = "disposition"
		} else if s.CodecType == "audio" && s.Disposition.Default == 1 {
			audioDefaultStreamIndex = totalAudioStreams - 1
		}

		if s.CodecType == "audio" && s.CodecName == "aac" && s.Channels != 2 && process == "" {
			process = "channelToStereo"
			audioBitrate = s.Bitrate
			audioStreamIndex = totalAudioStreams - 1
		}

		if s.CodecType == "audio" && s.CodecName != "aac" && process == "" {
			process = "encode"
			audioStreamIndex = totalAudioStreams - 1
		}

	}

	// Run needed ffmpeg commands
	if process == "disposition" {
		// Rename file to .original
		originalFile := fmt.Sprintf("%v.original", filePath)

		err = os.Rename(filePath, originalFile)
		if err != nil {
			panic(err)
		}

		changeDefaultAudioStream(totalAudioStreams, audioStreamIndex, audioDefaultStreamIndex, originalFile, filePath)
	}
	if process == "channelToStereo" {
		// Rename file to .original
		originalFile := fmt.Sprintf("%v.original", filePath)

		err = os.Rename(filePath, originalFile)
		if err != nil {
			panic(err)
		}

		createStereoAudioStream(totalAudioStreams, audioStreamIndex, audioBitrate, originalFile, filePath)
	}
	if process == "encode" {
		// Rename file to .original
		originalFile := fmt.Sprintf("%v.original", filePath)

		err = os.Rename(filePath, originalFile)
		if err != nil {
			panic(err)
		}

		encodeAudioStream(totalAudioStreams, audioStreamIndex, originalFile, filePath)
	}

	return nil
}

func extractSubs(language string, originalFile string, subStreamIndex int, customNamingTag string, filePath string) error {

	fileName := filepath.Base(filePath)
	fileDir := filepath.Dir(filePath)

	input := ffmpeg.Input(originalFile, ffmpeg.KwArgs{"sub_charenc": "Latin1"})

	subtitleIndex := fmt.Sprintf("s:%v", subStreamIndex)
	subtitle := input.Get(subtitleIndex)

	// Convert subtitles to vtt
	subtitleFileName := fmt.Sprintf("%v.%v%v.vtt", strings.TrimSuffix(fileName, filepath.Ext(fileName)), language, customNamingTag)
	fmt.Printf("Extracting Subtitle: %v", subtitleFileName)

	outputFileDir := fmt.Sprintf("%v/%v", fileDir, subtitleFileName)

	codecSubtitleStream := fmt.Sprintf("c:s:%v", subStreamIndex)
	out := ffmpeg.Output([]*ffmpeg.Stream{subtitle}, outputFileDir, ffmpeg.KwArgs{codecSubtitleStream: "webvtt"}).OverWriteOutput()

	out.Run()
	return nil
}

func changeDefaultAudioStream(totalAudioStreams, audioStreamIndex, audioDefaultStreamIndex int, originalFile, filePath string) error {
	fmt.Printf("\nChanging a:%v to default\n", audioStreamIndex)

	fileName := filepath.Base(filePath)
	fileDir := filepath.Dir(filePath)

	fileOutput := fmt.Sprintf("%v/%v.mp4", fileDir, strings.TrimSuffix(fileName, filepath.Ext(fileName)))

	input := ffmpeg.Input(originalFile)

	var streams []*ffmpeg.Stream

	streams = append(streams, input.Get("v:0")) // Append video stream to slice
	for i := 0; i <= totalAudioStreams-1; i++ {
		stream := fmt.Sprintf("a:%v", i)

		streams = append(streams, input.Get(stream))
	}

	newDefaultAudioStream := fmt.Sprintf("disposition:a:%v", audioStreamIndex)
	oldDefaultAudioStream := fmt.Sprintf("disposition:a:%v", audioDefaultStreamIndex)

	out := ffmpeg.Output(streams, fileOutput, ffmpeg.KwArgs{"c": "copy", newDefaultAudioStream: "default", oldDefaultAudioStream: 0, "movflags": "faststart"}).OverWriteOutput()
	out.Run()

	os.Remove(originalFile)
	return nil
}

func createStereoAudioStream(totalAudioStreams, audioStreamIndex int, audioBitrate, originalFile, filePath string) error {
	fmt.Println("Creating AAC 2.0 audio stream.")

	fileName := filepath.Base(filePath)
	fileDir := filepath.Dir(filePath)

	fileOutput := fmt.Sprintf("%v/%v.mp4", fileDir, strings.TrimSuffix(fileName, filepath.Ext(fileName)))

	input := ffmpeg.Input(originalFile)

	var streams []*ffmpeg.Stream

	streams = append(streams, input.Get("v")) // Get video stream
	streams = append(streams, input.Get("a")) // Get all audio streams

	for i := 0; i <= totalAudioStreams-1; i++ {
		stream := fmt.Sprintf("a:%v", i)

		// only append the stream that is going to be downmixed to stereo and copied to a new audio stream
		if i == audioStreamIndex {
			streams = append(streams, input.Get(stream))
		}
	}

	addedAudioStream := fmt.Sprintf("c:a:%v", totalAudioStreams)

	// audio bitrate string to int
	audioBitrateInt, err := strconv.Atoi(audioBitrate)
	if err != nil {
		// ... handle error
		panic(err)
	}

	out := ffmpeg.Output(streams, fileOutput, ffmpeg.KwArgs{"c:v": "copy", "ac": 2, "b:a:0": audioBitrateInt, addedAudioStream: "copy", "disposition:a": 0, "disposition:a:0": "default", "movflags": "faststart"}).OverWriteOutput()
	out.Run()

	os.Remove(originalFile)

	return nil
}

func encodeAudioStream(totalAudioStreams, audioStreamIndex int, originalFile, filePath string) error {
	fmt.Println("Converting and creating new AAC 2.0 audio stream.")

	fileName := filepath.Base(filePath)
	fileDir := filepath.Dir(filePath)

	fileOutput := fmt.Sprintf("%v/%v.mp4", fileDir, strings.TrimSuffix(fileName, filepath.Ext(fileName)))

	input := ffmpeg.Input(originalFile)

	var streams []*ffmpeg.Stream

	streams = append(streams, input.Get("v")) // Get video stream
	streams = append(streams, input.Get("a")) // Get all audio streams

	for i := 0; i <= totalAudioStreams-1; i++ {
		stream := fmt.Sprintf("a:%v", i)

		// only append the stream that is going to be copied
		if i == audioStreamIndex {
			streams = append(streams, input.Get(stream))
		}
	}

	addedAudioStream := fmt.Sprintf("c:a:%v", totalAudioStreams)

	out := ffmpeg.Output(streams, fileOutput, ffmpeg.KwArgs{"c:v": "copy", "c:a:0": "aac", "ac": 2, addedAudioStream: "copy", "disposition:a": 0, "disposition:a:0": "default", "movflags": "faststart"}).OverWriteOutput()
	out.Run()

	os.Remove(originalFile)

	return nil
}
