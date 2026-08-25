package pktline

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

var ErrFlush = errors.New("pkt-line flush")

const MaxPayload = 65516

type Reader struct{ r *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReader(r)} }

func (r *Reader) Read() ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r.r, header); err != nil {
		return nil, fmt.Errorf("read pkt-line header: %w", err)
	}
	length64, err := strconv.ParseUint(string(header), 16, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid pkt-line length %q: %w", header, err)
	}
	length := int(length64)
	if length == 0 {
		return nil, ErrFlush
	}
	if length < 4 || length-4 > MaxPayload {
		return nil, fmt.Errorf("invalid pkt-line length %d", length)
	}
	payload := make([]byte, length-4)
	if _, err := io.ReadFull(r.r, payload); err != nil {
		return nil, fmt.Errorf("read pkt-line payload: %w", err)
	}
	return payload, nil
}

func Write(w io.Writer, payload []byte) error {
	if len(payload) > MaxPayload {
		return errors.New("pkt-line payload too large")
	}
	if _, err := fmt.Fprintf(w, "%04x", len(payload)+4); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func Flush(w io.Writer) error { _, err := io.WriteString(w, "0000"); return err }
