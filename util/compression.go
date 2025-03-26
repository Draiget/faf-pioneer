package util

import (
	"bytes"
	"compress/zlib"
	"io"
)

func DeflateDecompressData(data []byte) ([]byte, error) {
	b := bytes.NewReader(data)
	r, err := zlib.NewReader(b)
	if err != nil {
		return nil, err
	}

	defer func(r io.ReadCloser) {
		_ = r.Close()
	}(r)

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	if err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}
