package media

import (
	"encoding/json"
	"strconv"
	"strings"
)

type ProbeData struct {
	Streams []Stream    `json:"streams"`
	Format  ProbeFormat `json:"format"`
}

type ProbeFormat struct {
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	FormatName string `json:"format_name"`
}

type Stream struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	Channels    int               `json:"channels"`
	Tags        map[string]string `json:"tags"`
	Disposition Disposition       `json:"disposition"`
}

type Disposition struct {
	Default         int `json:"default"`
	Forced          int `json:"forced"`
	HearingImpaired int `json:"hearing_impaired"`
	VisualImpaired  int `json:"visual_impaired"`
	Descriptions    int `json:"descriptions"`
	AttachedPic     int `json:"attached_pic"`
}

func ParseProbe(raw []byte) (ProbeData, error) {
	var probe ProbeData
	err := json.Unmarshal(raw, &probe)
	return probe, err
}

func (p ProbeData) OrdinaryVideoStreams() []Stream {
	return p.streamsByType("video", func(stream Stream) bool {
		return stream.Disposition.AttachedPic == 0
	})
}

func (p ProbeData) AudioStreams() []Stream {
	return p.streamsByType("audio", nil)
}

func (p ProbeData) SubtitleStreams() []Stream {
	return p.streamsByType("subtitle", nil)
}

func (p ProbeData) AttachmentStreams() []Stream {
	return p.streamsByType("attachment", nil)
}

func (p ProbeData) AudioSignature() string {
	streams := p.AudioStreams()
	parts := make([]string, 0, len(streams))

	for _, stream := range streams {
		parts = append(parts, strings.Join([]string{
			strconv.Itoa(stream.Index),
			strings.ToLower(stream.CodecName),
			strconv.Itoa(stream.Channels),
			stream.Tags["language"],
			stream.Tags["title"],
			stream.Disposition.String(),
		}, ":"))
	}

	return strings.Join(parts, "|")
}

func (p ProbeData) VideoSignature() string {
	streams := p.OrdinaryVideoStreams()
	parts := make([]string, 0, len(streams))

	for _, stream := range streams {
		parts = append(parts, strings.Join([]string{
			strconv.Itoa(stream.Index),
			strings.ToLower(stream.CodecName),
			strconv.Itoa(stream.Width) + "x" + strconv.Itoa(stream.Height),
		}, ":"))
	}

	return strings.Join(parts, "|")
}

func (p ProbeData) DurationSeconds() float64 {
	duration, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil {
		return 0
	}
	return duration
}

func (p ProbeData) SizeBytes() int64 {
	size, err := strconv.ParseInt(p.Format.Size, 10, 64)
	if err != nil {
		return 0
	}
	return size
}

func (p ProbeData) streamsByType(codecType string, keep func(Stream) bool) []Stream {
	streams := make([]Stream, 0)
	for _, stream := range p.Streams {
		if stream.CodecType != codecType {
			continue
		}
		if keep != nil && !keep(stream) {
			continue
		}
		streams = append(streams, stream)
	}
	return streams
}

func (d Disposition) String() string {
	flags := make([]string, 0, 5)
	if d.Default == 1 {
		flags = append(flags, "default")
	}
	if d.Forced == 1 {
		flags = append(flags, "forced")
	}
	if d.HearingImpaired == 1 {
		flags = append(flags, "hearing_impaired")
	}
	if d.VisualImpaired == 1 {
		flags = append(flags, "visual_impaired")
	}
	if d.Descriptions == 1 {
		flags = append(flags, "descriptions")
	}
	return strings.Join(flags, "+")
}
