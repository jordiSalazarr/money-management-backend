// AWS Transcribe Streaming backend for the voice slice.
//
// Why streaming over batch: batch Transcribe requires uploading audio
// to S3 then polling a job — minimum end-to-end latency is 20-30s for
// even the shortest clips. The streaming API accepts PCM frames over
// HTTP/2 event streams and returns transcripts in ~1-2s, which is the
// only viable shape for a "tap, speak, get answer" UX.
//
// The frontend records a complete WAV blob (because MediaRecorder gives
// us a discrete file, not a live PCM tap). We strip the WAV header,
// then push the raw PCM bytes into the streaming API as if we were
// recording live. AWS doesn't care that the audio is already complete.

package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	tstypes "github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
)

const (
	defaultLanguageCode = "en-US"
	// audioChunkBytes is the size of each PCM chunk pushed to
	// Transcribe. The streaming API caps individual events at 1 MB;
	// 8 KB keeps events small and lets results stream back while the
	// upload is still in flight.
	audioChunkBytes = 8192
)

type awsTranscribeBackend struct {
	client       *transcribestreaming.Client
	languageCode tstypes.LanguageCode
}

func newAWSTranscribeBackend(ctx context.Context, language string) (*awsTranscribeBackend, error) {
	if language == "" {
		language = defaultLanguageCode
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading aws config for transcribe: %w", err)
	}
	return &awsTranscribeBackend{
		client:       transcribestreaming.NewFromConfig(cfg),
		languageCode: tstypes.LanguageCode(language),
	}, nil
}

func (t *awsTranscribeBackend) Transcribe(ctx context.Context, wav []byte) (string, error) {
	pcm, sampleRate, err := parseWAV(wav)
	if err != nil {
		return "", fmt.Errorf("parsing wav: %w", err)
	}

	out, err := t.client.StartStreamTranscription(ctx, &transcribestreaming.StartStreamTranscriptionInput{
		LanguageCode:         t.languageCode,
		MediaSampleRateHertz: aws.Int32(sampleRate),
		MediaEncoding:        tstypes.MediaEncodingPcm,
	})
	if err != nil {
		return "", fmt.Errorf("starting transcribe stream: %w", err)
	}

	stream := out.GetStream()
	defer stream.Close()

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- pushAudio(ctx, stream, pcm)
	}()

	// Two buckets:
	//   finals    — IsPartial == false. Preferred.
	//   partial   — the most recent IsPartial == true text per result-id.
	// If the close-end-of-stream race lands such that Transcribe sends
	// partials but no final before the stream tears down, we fall back
	// to the latest partial instead of returning an empty string.
	var finals strings.Builder
	latestPartial := map[string]string{}
	events, partials, finalCount := 0, 0, 0

	for event := range stream.Events() {
		events++
		te, ok := event.(*tstypes.TranscriptResultStreamMemberTranscriptEvent)
		if !ok {
			continue
		}
		for _, r := range te.Value.Transcript.Results {
			if len(r.Alternatives) == 0 || r.Alternatives[0].Transcript == nil {
				continue
			}
			text := *r.Alternatives[0].Transcript
			id := aws.ToString(r.ResultId)
			if r.IsPartial {
				partials++
				latestPartial[id] = text
				continue
			}
			finalCount++
			if finals.Len() > 0 {
				finals.WriteByte(' ')
			}
			finals.WriteString(text)
			delete(latestPartial, id) // final superseded the partial
		}
	}

	if err := <-sendErr; err != nil {
		return "", fmt.Errorf("sending audio: %w", err)
	}
	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("transcribe stream: %w", err)
	}

	transcript := strings.TrimSpace(finals.String())
	usedFallback := false
	if transcript == "" && len(latestPartial) > 0 {
		// No finals arrived. Concatenate the latest partial of each
		// result-id. Map iteration order isn't stable but for short
		// commands there's usually one result so order doesn't matter.
		var pb strings.Builder
		for _, v := range latestPartial {
			if pb.Len() > 0 {
				pb.WriteByte(' ')
			}
			pb.WriteString(v)
		}
		transcript = strings.TrimSpace(pb.String())
		usedFallback = true
	}

	slog.Info("transcribe result",
		"language", string(t.languageCode),
		"pcm_bytes", len(pcm),
		"events_total", events,
		"partials_received", partials,
		"finals_received", finalCount,
		"used_partial_fallback", usedFallback,
		"text", transcript,
	)
	return transcript, nil
}

// pushAudio streams PCM bytes to Transcribe and closes only the input
// half so the service knows there's no more audio coming but the
// reader stays alive long enough to receive the final transcript.
//
// Calling stream.Close() here would be wrong: the SDK's safeClose
// closes the writer (waiting at most 1s) and then immediately closes
// the reader, which ends Events() before Transcribe has had time to
// finalise and send the last TranscriptEvent. That manifested as
// events_total=0 in the Lambda log.
func pushAudio(ctx context.Context, stream *transcribestreaming.StartStreamTranscriptionEventStream, pcm []byte) error {
	for off := 0; off < len(pcm); off += audioChunkBytes {
		end := off + audioChunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		chunk := make([]byte, end-off)
		copy(chunk, pcm[off:end])
		err := stream.Send(ctx, &tstypes.AudioStreamMemberAudioEvent{
			Value: tstypes.AudioEvent{AudioChunk: chunk},
		})
		if err != nil {
			return err
		}
	}
	return stream.Writer.Close()
}

// parseWAV walks RIFF/WAVE chunks and returns the PCM data plus the
// sample rate. Tolerant of optional chunks (LIST, fact, etc.) that
// some browser WAV encoders insert between fmt and data.
func parseWAV(wav []byte) (pcm []byte, sampleRate int32, err error) {
	if len(wav) < 44 {
		return nil, 0, fmt.Errorf("wav too short: %d bytes", len(wav))
	}
	if !bytes.Equal(wav[0:4], []byte("RIFF")) || !bytes.Equal(wav[8:12], []byte("WAVE")) {
		return nil, 0, fmt.Errorf("not a RIFF/WAVE file")
	}

	r := bytes.NewReader(wav[12:])
	var rate int32
	var fmtSeen bool
	for {
		var id [4]byte
		if _, err := io.ReadFull(r, id[:]); err == io.EOF {
			break
		} else if err != nil {
			return nil, 0, err
		}
		var size uint32
		if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
			return nil, 0, err
		}
		chunk := make([]byte, size)
		if _, err := io.ReadFull(r, chunk); err != nil {
			return nil, 0, err
		}
		switch string(id[:]) {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, 0, fmt.Errorf("fmt chunk too short")
			}
			audioFormat := binary.LittleEndian.Uint16(chunk[0:2])
			channels := binary.LittleEndian.Uint16(chunk[2:4])
			rate = int32(binary.LittleEndian.Uint32(chunk[4:8]))
			bits := binary.LittleEndian.Uint16(chunk[14:16])
			if audioFormat != 1 {
				return nil, 0, fmt.Errorf("unsupported wav format %d (PCM=1 required)", audioFormat)
			}
			if channels != 1 {
				return nil, 0, fmt.Errorf("unsupported channel count %d (mono required)", channels)
			}
			if bits != 16 {
				return nil, 0, fmt.Errorf("unsupported sample width %d bits (16 required)", bits)
			}
			fmtSeen = true
		case "data":
			if !fmtSeen {
				return nil, 0, fmt.Errorf("data chunk before fmt chunk")
			}
			return chunk, rate, nil
		}
		// Chunks are word-aligned: pad byte if odd-sized.
		if size%2 == 1 {
			if _, err := r.ReadByte(); err != nil && err != io.EOF {
				return nil, 0, err
			}
		}
	}
	return nil, 0, fmt.Errorf("no data chunk in wav")
}
