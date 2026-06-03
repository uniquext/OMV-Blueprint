package media

import "strconv"

func BuildFFmpegArgs(input string, output string, probe ProbeData, decision Decision, includeDataStreams bool) []string {
	args := []string{
		"-hide_banner", "-y", "-i", input,
		"-map", "0:V", "-c:v", "copy",
	}

	audioStreams := probe.AudioStreams()
	audioPlans := audioPlansOrCopyFallback(audioStreams, decision.AudioPlans)
	for outputAudioIndex, plan := range audioPlans {
		audioIndex := itoa(outputAudioIndex)
		args = append(args, "-map", "0:a:"+itoa(plan.InputAudioOrdinal))

		if plan.Transcode {
			args = append(args, "-c:a:"+audioIndex, "aac", "-b:a:"+audioIndex, plan.Bitrate)
		} else {
			args = append(args, "-c:a:"+audioIndex, "copy")
		}

		if plan.InputAudioOrdinal < len(audioStreams) {
			stream := audioStreams[plan.InputAudioOrdinal]
			if stream.Tags["language"] != "" {
				args = append(args, "-metadata:s:a:"+audioIndex, "language="+stream.Tags["language"])
			}
			if stream.Tags["title"] != "" {
				args = append(args, "-metadata:s:a:"+audioIndex, "title="+stream.Tags["title"])
			}
			args = append(args, "-disposition:a:"+audioIndex, dispositionArg(stream.Disposition))
		}
	}

	args = append(args,
		"-map", "0:s?", "-c:s", "copy",
		"-map", "0:t?", "-c:t", "copy",
	)
	if includeDataStreams {
		args = append(args, "-map", "0:d?", "-c:d", "copy")
	}
	args = append(args, "-max_muxing_queue_size", "9999", output)

	return args
}

func audioPlansOrCopyFallback(audioStreams []Stream, audioPlans []AudioPlan) []AudioPlan {
	if validAudioPlans(audioStreams, audioPlans) {
		return audioPlans
	}

	copyPlans := make([]AudioPlan, 0, len(audioStreams))
	for ordinal, stream := range audioStreams {
		copyPlans = append(copyPlans, AudioPlan{
			InputAudioOrdinal: ordinal,
			StreamIndex:       stream.Index,
			Codec:             stream.CodecName,
			Channels:          stream.Channels,
			Transcode:         false,
		})
	}
	return copyPlans
}

func validAudioPlans(audioStreams []Stream, audioPlans []AudioPlan) bool {
	if len(audioPlans) != len(audioStreams) {
		return false
	}

	seen := make(map[int]bool, len(audioPlans))
	for _, plan := range audioPlans {
		if plan.InputAudioOrdinal < 0 || plan.InputAudioOrdinal >= len(audioStreams) {
			return false
		}
		if seen[plan.InputAudioOrdinal] {
			return false
		}
		seen[plan.InputAudioOrdinal] = true
	}
	return true
}

func dispositionArg(d Disposition) string {
	disposition := d.String()
	if disposition == "" {
		return "0"
	}
	return disposition
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
