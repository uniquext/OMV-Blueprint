package media

import "fmt"

func Fingerprint(path string, size int64, mtimeNS int64, audioSignature string, videoSignature string) string {
	return fmt.Sprintf("%s|%d|%d|%s|%s", path, size, mtimeNS, audioSignature, videoSignature)
}
