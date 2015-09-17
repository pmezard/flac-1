// +build gofuzz
package flac

import (
	"bytes"
)

func Fuzz(data []byte) int {
	stream, err := Parse(bytes.NewBuffer(data))
	if err != nil {
		return 0
	}
	for {
		_, err := stream.ParseNext()
		if err != nil {
			break
		}
	}
	return 0
}
