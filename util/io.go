package util

import (
	"context"
	"io"
)

func NewCancelableIoReader(ctx context.Context, r io.Reader) *io.PipeReader {
	pr, pw := io.Pipe()
	go func() {
		defer func(pw *io.PipeWriter) {
			_ = pw.Close()
		}(pw)

		buf := make([]byte, 1024)

		for {
			type readResult struct {
				n   int
				err error
			}

			// Channel for read result
			resultChannel := make(chan readResult, 1)

			// "Threaded" caller for reading buffer (stdin or any other io.Reader).
			go func() {
				n, err := r.Read(buf)
				resultChannel <- readResult{n, err}
			}()

			// Now we had to select, either context was canceled, and we will close our pipe,
			// or get result from io.Reader.
			select {
			case <-ctx.Done():
				// If context canceled, close pipe writer with context-error message.
				_ = pw.CloseWithError(ctx.Err())
				return
			case res := <-resultChannel:
				// If some data was read, write it to pipe writer then.
				if res.n > 0 {
					if _, err := pw.Write(buf[:res.n]); err != nil {
						return
					}
				}
				if res.err != nil {
					_ = pw.CloseWithError(res.err)
					return
				}
			}
		}
	}()

	return pr
}
