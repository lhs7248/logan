// Package protocol implements the existing Logan binary wire format.
package protocol

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

var ErrLimit = errors.New("protocol resource limit exceeded")
var ErrFrame = errors.New("invalid Logan encrypted frame")

type Limits struct{ MaxInput, MaxFrame int64 }
type NativeDecoder struct {
	key, iv []byte
	limits  Limits
}

func NewNativeDecoder(key, iv []byte, limits Limits) (*NativeDecoder, error) {
	if len(key) != 16 || len(iv) != 16 {
		return nil, fmt.Errorf("native AES key and IV must each contain 16 bytes")
	}
	if limits.MaxInput <= 0 || limits.MaxFrame <= 0 || limits.MaxFrame > limits.MaxInput {
		return nil, fmt.Errorf("invalid native protocol limits")
	}
	return &NativeDecoder{append([]byte(nil), key...), append([]byte(nil), iv...), limits}, nil
}

type budgetReader struct {
	ctx  context.Context
	r    io.Reader
	left int64
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > r.left+1 {
		p = p[:r.left+1]
	}
	n, err := r.r.Read(p)
	if int64(n) > r.left {
		return 0, ErrLimit
	}
	r.left -= int64(n)
	return n, err
}
func (d *NativeDecoder) Decode(ctx context.Context, src io.Reader, dst io.Writer) error {
	r := bufio.NewReader(&budgetReader{ctx: ctx, r: src, left: d.limits.MaxInput})
	block, _ := aes.NewCipher(d.key)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		marker, err := r.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if marker != 1 {
			continue
		}
		var header [4]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return ErrFrame
		}
		length := int64(int32(binary.BigEndian.Uint32(header[:])))
		if length <= 0 || length%aes.BlockSize != 0 {
			return ErrFrame
		}
		if length > d.limits.MaxFrame {
			return ErrLimit
		}
		if err := d.frame(ctx, r, dst, block, length); err != nil {
			return err
		}
	}
}
func (d *NativeDecoder) frame(ctx context.Context, r io.Reader, w io.Writer, block cipher.Block, length int64) error {
	mode := cipher.NewCBCDecrypter(block, d.iv)
	var buffer [64 * 1024]byte
	var lastCipher, lastPlain [aes.BlockSize]byte
	for length > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := int64(len(buffer))
		if n > length {
			n = length
		}
		part := buffer[:int(n)]
		if _, err := io.ReadFull(r, part); err != nil {
			return ErrFrame
		}
		copy(lastCipher[:], part[len(part)-aes.BlockSize:])
		mode.CryptBlocks(part, part)
		length -= n
		if length == 0 {
			copy(lastPlain[:], part[len(part)-aes.BlockSize:])
			part = part[:len(part)-aes.BlockSize]
		}
		if err := writeAll(w, part); err != nil {
			return err
		}
	}
	// A frame may be an unpadded fragment of a gzip stream. Its real data
	// can end in valid-looking PKCS#7, so preserve bytes here. Decompress
	// recognizes alignment only after a complete gzip member, where it is
	// unambiguously outside the compressed payload.
	// Retain the existing rejection of the reference's positive padding probe
	// with an invalid full CBC tail; do not broaden malformed-frame acceptance.
	var probe [aes.BlockSize]byte
	cipher.NewCBCDecrypter(block, d.iv).CryptBlocks(probe[:], lastCipher[:])
	tail := lastPlain[:]
	if validPadding(probe[:]) && !validPadding(tail) {
		return ErrFrame
	}
	return writeAll(w, tail)
}
func validPadding(b []byte) bool {
	n := int(b[len(b)-1])
	if n < 1 || n > aes.BlockSize || n > len(b) {
		return false
	}
	for _, v := range b[len(b)-n:] {
		if int(v) != n {
			return false
		}
	}
	return true
}
func writeAll(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

// Decompress writes bounded plaintext to a private temporary file. On a damaged
// member it truncates to the last complete newline, as the Java implementation does.
// Trailing non-GZIP data after a valid member is ignored by Java's GZIPInputStream.
func Decompress(ctx context.Context, src io.Reader, dst *os.File, maxPlain int64) error {
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
	var total, lastLF int64
	members := 0
	trim := func() error {
		if err := dst.Truncate(lastLF); err != nil {
			return err
		}
		_, err := dst.Seek(lastLF, 0)
		return err
	}
	var buffer [32 * 1024]byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Historical uploaded objects may already contain alignment padding.
		// Skip at most one block, only between completed members and only when
		// the exact padding is followed by another gzip header. Never scan junk.
		if members > 0 {
			peek, _ := r.Peek(aes.BlockSize + 3)
			gap := 0
			if len(peek) > 0 && peek[0] == 0 {
				for gap < aes.BlockSize-1 && gap < len(peek) && peek[gap] == 0 {
					gap++
				}
			} else if len(peek) > 0 && peek[0] <= aes.BlockSize {
				n := int(peek[0])
				if n <= len(peek) && validPadding(peek[:n]) {
					gap = n
				}
			}
			if gap > 0 && len(peek) >= gap+3 && bytes.Equal(peek[gap:gap+3], []byte{0x1f, 0x8b, 8}) {
				_, _ = r.Discard(gap)
			}
		}
		gz, err := gzip.NewReader(r)
		if err != nil {
			if members > 0 {
				return nil
			}
			return trim()
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
				if pos := bytes.LastIndexByte(buffer[:n], '\n'); pos >= 0 {
					lastLF = total + int64(pos) + 1
				}
				total += int64(n)
			}
			if readErr != nil {
				_ = gz.Close()
				if readErr != io.EOF {
					return trim()
				}
				break
			}
		}
		members++
	}
}
