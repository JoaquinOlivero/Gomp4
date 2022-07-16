package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
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
	var filePath string

	fmt.Println("Please insert path to video file:")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		filePath = scanner.Text()
	}

	originalFile := fmt.Sprintf("%v.original", filePath)

	err := os.Rename(filePath, originalFile)
	if err != nil {
		panic(err)
	}
	data, err := ffmpeg.Probe(originalFile)
	if err != nil {
		panic(err)
	}

	err = convert(data, originalFile, filePath)
	if err != nil {
		fmt.Println(err)
	}

}

func convert(fileData string, originalFile string, filePath string) error {

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

		// Check that file codec is h264. h265 transcoding to h264 not supported.
		if s.CodecType == "video" && s.CodecName != "h264" {
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

			extractSubs(s.Tags.Language, originalFile, subStreamIndex, customNamingTag, filePath)
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
		changeDefaultAudioStream(totalAudioStreams, audioStreamIndex, audioDefaultStreamIndex, originalFile, filePath)
	}
	if process == "channelToStereo" {
		createStereoAudioStream(totalAudioStreams, audioStreamIndex, audioBitrate, originalFile, filePath)
	}
	if process == "encode" {
		encodeAudioStream(totalAudioStreams, audioStreamIndex, originalFile, filePath)
	}

	return nil
}

func extractSubs(language string, originalFile string, subStreamIndex int, customNamingTag string, filePath string) error {
	fileName := filepath.Base(filePath)
	fileDir := filepath.Dir(filePath)

	input := ffmpeg.Input(originalFile)

	subtitleIndex := fmt.Sprintf("s:%v", subStreamIndex)
	subtitle := input.Get(subtitleIndex)

	subtitleFileName := fmt.Sprintf("%v.%v%v.srt", strings.TrimSuffix(fileName, filepath.Ext(fileName)), language, customNamingTag)
	fmt.Printf("Extracting Subtitle: %v", subtitleFileName)

	outputFileDir := fmt.Sprintf("%v/%v", fileDir, subtitleFileName)

	out := ffmpeg.Output([]*ffmpeg.Stream{subtitle}, outputFileDir).OverWriteOutput()

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

	out := ffmpeg.Output(streams, fileOutput, ffmpeg.KwArgs{"c": "copy", newDefaultAudioStream: "default", oldDefaultAudioStream: 0, "movflags": "+faststart"}).OverWriteOutput()
	out.Run()

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

	out := ffmpeg.Output(streams, fileOutput, ffmpeg.KwArgs{"c:v": "copy", "ac": 2, "b:a:0": audioBitrateInt, addedAudioStream: "copy", "disposition:a": 0, "disposition:a:0": "default", "movflags": "+faststart"}).OverWriteOutput()
	out.Run()

	return nil
}

func encodeAudioStream(totalAudioStreams, audioStreamIndex int, originalFile, filePath string) error {
	fmt.Println("Encoding AAC 2.0 audio stream.")

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

	out := ffmpeg.Output(streams, fileOutput, ffmpeg.KwArgs{"c:v": "copy", "c:a:0": "aac", "ac": 2, addedAudioStream: "copy", "disposition:a": 0, "disposition:a:0": "default", "movflags": "+faststart"}).OverWriteOutput().ErrorToStdOut()
	out.Run()

	return nil
}
