package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"

	webmsaver "baiest.github.com/serverWebRTC/saver"
)

var (
	peerConnection webrtc.PeerConnection
	tracks         = map[string]*webrtc.TrackRemote{}
)

const MAX_VIDEO_DURATION = 10

type WebRTCRequest struct {
	ProcessID string                    `json:"process_id"`
	Offer     webrtc.SessionDescription `json:"offer"`
}

func enableCors(rw *http.ResponseWriter) {
	fmt.Println("CORS")
	(*rw).Header().Set("Access-Control-Allow-Origin", "*")
	(*rw).Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	(*rw).Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func recordVideo(track *webrtc.TrackRemote, filename string) {
	fmt.Println("Mimetype:", track.Codec().MimeType)

	mimeType := map[string]rtp.Depacketizer{
		"video/VP8": &codecs.VP8Packet{},
	}

	saver := webmsaver.NewWebmSaver(mimeType[track.Codec().RTPCodecCapability.MimeType])

	now := time.Now()
	ellapse := time.Since(now)

	for int(ellapse.Seconds()) < MAX_VIDEO_DURATION {
		ellapse = time.Since(now)
		fmt.Println(ellapse)
		// Read RTP packets being sent to Pion
		rtp, _, readErr := track.ReadRTP()
		if readErr != nil {

			if readErr == io.EOF {
				fmt.Println("Closing...")

				saver.Close()
				return
			}
			fmt.Println(readErr)
		}

		switch track.Kind() {
		case webrtc.RTPCodecTypeVideo:
			saver.PushChuncks(rtp, filename)
		}
	}
}

func handleOnTrack(peerConnection *webrtc.PeerConnection, processID string) func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	return func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		go func() {
			ticker := time.NewTicker(time.Second * 3)
			for range ticker.C {
				errSend := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
				if errSend != nil {
					fmt.Println(errSend)
					return
				}
			}
		}()

		fmt.Printf("Track has started, of type %d: %s \n", track.PayloadType(), track.Codec().RTPCodecCapability.MimeType)

		tracks[processID] = track
		fmt.Println(tracks, processID)
	}
}

func createWebRTCConn(config webrtc.Configuration) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		enableCors(&rw)

		if r.Method != http.MethodPost {
			rw.WriteHeader(http.StatusOK)
			return
		}

		req := WebRTCRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, "Failed to decode SDP", http.StatusBadRequest)
			return
		}

		// Create a MediaEngine object to configure the supported codec
		m := &webrtc.MediaEngine{}

		// Setup the codecs you want to use.
		// Only support VP8 and OPUS, this makes our WebM muxer code simpler
		if err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: "video/VP8", ClockRate: 90000, Channels: 0, SDPFmtpLine: "", RTCPFeedback: nil},
			PayloadType:        96,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			panic(err)
		}

		api := webrtc.NewAPI(webrtc.WithMediaEngine(m))

		// Create a new RTCPeerConnection
		peerConnection, err := api.NewPeerConnection(config)
		if err != nil {
			panic(err)
		}

		responseChannel := make(chan *webrtc.ICECandidate)
		peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
			if i != nil {
				return
			}

			responseChannel <- i
			fmt.Printf("New candidate %s\n", i)
		})

		peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			fmt.Printf("ICE Connection State has changed to %s\n", connectionState.String())

			if connectionState == webrtc.ICEConnectionStateConnected {
				peerConnection.OnTrack(handleOnTrack(peerConnection, req.ProcessID))
				return
			}

			if connectionState == webrtc.ICEConnectionStateDisconnected {
				// Cliente desconectado, realiza la limpieza necesaria
				if err := peerConnection.Close(); err != nil {
					fmt.Println("Close ICE error:", err.Error())
				}
			}
		})

		peerConnection.OnDataChannel(func(dc *webrtc.DataChannel) {
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				fmt.Printf("New Message: %s\n", msg.Data)

				message := strings.Split(string(msg.Data), ";")
				if message[1] == "start" {
					recordVideo(tracks[message[0]], message[0])
					if err := peerConnection.Close(); err != nil {
						fmt.Println(err.Error())
						return
					}
					return
				}

				if message[1] == "stop" {
					if err := peerConnection.Close(); err != nil {
						fmt.Println(err.Error())
						return
					}
				}

			})

			dc.OnOpen(func() {
				fmt.Println("Connection opeeeen!")
			})

		})

		fmt.Println(req.Offer.Type)
		if err := peerConnection.SetRemoteDescription(req.Offer); err != nil {
			fmt.Println(err.Error())
			http.Error(rw, "Failed to set remote description", http.StatusInternalServerError)
			return
		}

		answer, err := peerConnection.CreateAnswer(nil)
		peerConnection.SetLocalDescription(answer)

		<-responseChannel

		if err := json.NewEncoder(rw).Encode(peerConnection.LocalDescription()); err != nil {
			http.Error(rw, "Failed to encode and send answer", http.StatusInternalServerError)
			return
		}
	}
}

func main() {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	http.HandleFunc("/", createWebRTCConn(config))

	fmt.Println("Server listening on http://localhost:8080")
	http.ListenAndServe("0.0.0.0:8080", nil)
}
