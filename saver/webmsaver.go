package webmsaver

import (
	"fmt"
	"os"
	"time"

	"github.com/at-wat/ebml-go/webm"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3/pkg/media/samplebuilder"
)

type WebmSaver struct {
	audioWriter, videoWriter       webm.BlockWriteCloser
	audioBuilder, videoBuilder     *samplebuilder.SampleBuilder
	audioTimestamp, videoTimestamp time.Duration
}

func NewWebmSaver(codec rtp.Depacketizer) *WebmSaver {
	return &WebmSaver{
		videoBuilder: samplebuilder.New(10, codec, 90000),
	}
}

func (s *WebmSaver) Close() {
	fmt.Printf("Finalizing webm...\n")
	if s.videoWriter != nil {
		if err := s.videoWriter.Close(); err != nil {
			fmt.Println(err)
		}
	}
}

func (s *WebmSaver) InitWriter(filename string, width, height int) {
	w, err := os.OpenFile(filename+".webm", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	ws, err := webm.NewSimpleBlockWriter(w,
		[]webm.TrackEntry{
			{
				Name:        "Video",
				TrackNumber: 2,
				TrackUID:    67890,
				CodecID:     "V_VP8",
				TrackType:   1,
				Video: &webm.Video{
					PixelWidth:  uint64(width),
					PixelHeight: uint64(height),
				},
			},
		})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("WebM saver has started with video width=%d, height=%d\n", width, height)
	s.videoWriter = ws[0]
}

func (s *WebmSaver) PushChuncks(rtpPacket *rtp.Packet, filename string) {
	s.videoBuilder.Push(rtpPacket)

	for {
		sample := s.videoBuilder.Pop()
		if sample == nil {
			return
		}
		// Read VP8 header.
		videoKeyframe := (sample.Data[0]&0x1 == 0)
		if videoKeyframe {
			// Keyframe has frame information.
			raw := uint(sample.Data[6]) | uint(sample.Data[7])<<8 | uint(sample.Data[8])<<16 | uint(sample.Data[9])<<24
			width := int((raw >> 16) & 0x3FFF)
			height := int(raw & 0x3FFF)

			if s.videoWriter == nil {
				// Initialize WebM saver using received frame size.
				s.InitWriter(filename, width, height)
			}
		}
		if s.videoWriter != nil {
			s.videoTimestamp += sample.Duration
			if _, err := s.videoWriter.Write(videoKeyframe, int64(s.videoTimestamp/time.Millisecond), sample.Data); err != nil {
				fmt.Println(err)
				s.Close()
			}
		}
	}
}
