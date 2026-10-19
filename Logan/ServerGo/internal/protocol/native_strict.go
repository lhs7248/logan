package protocol

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"fmt"
	"io"
	"os"
)

// DecompressStrict is for controlled historical repair, where silently accepting
// only a prefix could turn damaged input into a misleading backfill plan. The
// serving path deliberately continues to use the legacy-tolerant Decompress.
func DecompressStrict(ctx context.Context, src io.Reader, dst *os.File, maxPlain int64) error {
	if maxPlain <= 0 {
		return ErrLimit
	}
	if err := dst.Truncate(0); err != nil {
		return err
	}
	if _, err := dst.Seek(0, 0); err != nil {
		return err
	}
	r := bufio.NewReader(src)
	var total int64
	members := 0
	var buffer [32 * 1024]byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		peek, peekErr := r.Peek(aes.BlockSize + 3)
		if peekErr != nil && peekErr != io.EOF {
			return peekErr
		}
		if len(peek) == 0 {
			if members > 0 {
				return nil
			}
			return ErrFrame
		}
		if members > 0 && peek[0] != 0x1f {
			gap := 0
			if peek[0] == 0 {
				for gap < len(peek) && gap < aes.BlockSize-1 && peek[gap] == 0 {
					gap++
				}
			} else if peek[0] <= aes.BlockSize {
				n := int(peek[0])
				if n <= len(peek) && validPadding(peek[:n]) {
					gap = n
				}
			}
			if gap == 0 {
				return ErrFrame
			}
			if len(peek) == gap && peekErr == io.EOF {
				return nil
			}
			if len(peek) < gap+3 || !bytes.Equal(peek[gap:gap+3], []byte{0x1f, 0x8b, 8}) {
				return ErrFrame
			}
			if _, err := r.Discard(gap); err != nil {
				return err
			}
		}
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("%w: incomplete gzip header", ErrFrame)
		}
		gz.Multistream(false)
		for {
			if err := ctx.Err(); err != nil {
				_ = gz.Close()
				return err
			}
			n, readErr := gz.Read(buffer[:])
			if int64(n) > maxPlain-total {
				_ = gz.Close()
				return ErrLimit
			}
			if n > 0 {
				if err := writeAll(dst, buffer[:n]); err != nil {
					_ = gz.Close()
					return err
				}
				total += int64(n)
			}
			if readErr != nil {
				_ = gz.Close()
				if readErr != io.EOF {
					return fmt.Errorf("%w: damaged gzip member", ErrFrame)
				}
				break
			}
		}
		members++
	}
}
