// Copyright 2019 Andrei Tudor Călin
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

package zerocopy

import (
	"errors"
	"io"
	"os"
)

// A Pipe is a buffered, unidirectional data channel.
type Pipe struct {
	r, w *os.File
}

// NewPipe creates a new pipe.
func NewPipe() (*Pipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &Pipe{r: r, w: w}, nil
}

// BufferSize returns the buffer size of the pipe.
func (p *Pipe) BufferSize() (int, error) {
	return 0, errNotImplemented
}

// SetBufferSize sets the pipe's buffer size to n.
func (p *Pipe) SetBufferSize(n int) error {
	return errNotImplemented
}

// Read reads data from the pipe.
func (p *Pipe) Read(b []byte) (n int, err error) {
	return p.r.Read(b)
}

// CloseRead closes the read side of the pipe.
func (p *Pipe) CloseRead() error {
	return p.r.Close()
}

// Write writes data to the pipe.
func (p *Pipe) Write(b []byte) (n int, err error) {
	return p.w.Write(b)
}

// CloseWrite closes the write side of the pipe.
func (p *Pipe) CloseWrite() error {
	return p.w.Close()
}

// Close closes both sides of the pipe.
func (p *Pipe) Close() error {
	err := p.r.Close()
	err1 := p.w.Close()
	if err != nil {
		return err
	}
	return err1
}

// ReadFrom transfers data from src to the pipe.
func (p *Pipe) ReadFrom(src io.Reader) (int64, error) {
	return p.readFrom(src)
}

// WriteTo transfers data from the pipe to dst.
func (p *Pipe) WriteTo(dst io.Writer) (int64, error) {
	return p.writeTo(dst)
}

// Transfer is like io.Copy, but moves data through the pipe rather than
// through a userspace buffer. Transfer is also like calling p.ReadFrom(src)
// and p.WriteTo(dst), but in lock-step, and using a single goroutine.
//
// Transfer uses splice(2) if possible.
func (p *Pipe) Transfer(dst io.Writer, src io.Reader) (int64, error) {
	return p.transfer(dst, src)
}

var errNotImplemented = errors.New("not implemented")
