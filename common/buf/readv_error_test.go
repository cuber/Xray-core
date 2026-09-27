//go:build !wasm && !openbsd

package buf

import (
	"errors"
	"io"
	"testing"
)

type readvResult struct {
	n   int32
	err error
}

type readvFaultReader struct {
	results []readvResult
	reads   int
	cleared bool
}

func (*readvFaultReader) Init([]*Buffer) {}
func (r *readvFaultReader) Read(uintptr) (int32, error) {
	result := r.results[r.reads]
	r.reads++
	return result.n, result.err
}
func (r *readvFaultReader) Clear() { r.cleared = true }

type readvPoll struct{ waits int }

func (*readvPoll) Control(func(uintptr)) error    { panic("unexpected Control") }
func (*readvPoll) Write(func(uintptr) bool) error { panic("unexpected Write") }
func (p *readvPoll) Read(f func(uintptr) bool) error {
	for range 3 {
		if f(0) {
			return nil
		}
		p.waits++
	}
	return errors.New("permanent error incorrectly sent back to poller")
}

func TestReadvErrorContract(t *testing.T) {
	reset := errors.New("connection reset")
	for _, tc := range []struct {
		name    string
		results []readvResult
		want    error
		bytes   int32
		waits   int
	}{
		{"reset", []readvResult{{0, reset}}, reset, 0, 0},
		{"eof", []readvResult{{0, nil}}, io.EOF, 0, 0},
		{"data", []readvResult{{3, nil}}, nil, 3, 0},
		{"would-block-then-data", []readvResult{{-1, nil}, {3, nil}}, nil, 3, 1},
		{"would-block-then-reset", []readvResult{{-1, nil}, {0, reset}}, reset, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			poll := &readvPoll{}
			mr := &readvFaultReader{results: tc.results}
			r := &ReadVReader{rawConn: poll, mr: mr, alloc: allocStrategy{current: 2}}
			mb, err := r.ReadMultiBuffer()
			defer ReleaseMulti(mb)
			if !errors.Is(err, tc.want) || mb.Len() != tc.bytes || poll.waits != tc.waits || !mr.cleared {
				t.Fatalf("err=%v bytes=%d waits=%d cleared=%v", err, mb.Len(), poll.waits, mr.cleared)
			}
		})
	}
}
