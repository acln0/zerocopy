// Copyright (c) 2019 Andrei Tudor Călin <mail@acln.ro>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zerocopy

import (
	"io"
	"os"
	"syscall"
)

// A Pipe is a buffered, unidirectional data channel.
type Pipe struct {
	r, w     *os.File
	rrc, wrc syscall.RawConn

	teerd   io.Reader
	teepipe *Pipe
}

// NewPipe creates a new pipe.
func NewPipe() (*Pipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	rrc, err := r.SyscallConn()
	if err != nil {
		return nil, err
	}
	wrc, err := w.SyscallConn()
	if err != nil {
		return nil, err
	}
	return &Pipe{
		r:     r,
		w:     w,
		rrc:   rrc,
		wrc:   wrc,
		teerd: r,
	}, nil
}

// BufferSize returns the buffer size of the pipe.
func (p *Pipe) BufferSize() (int, error) {
	return p.bufferSize()
}

// SetBufferSize sets the pipe's buffer size to n.
func (p *Pipe) SetBufferSize(n int) error {
	return p.setBufferSize(n)
}

// Read reads data from the pipe.
func (p *Pipe) Read(b []byte) (n int, err error) {
	return p.read(b)
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
//
// If src implements syscall.Conn, ReadFrom tries to use splice(2) for the
// data transfer from the source file descriptor to the pipe. If that is
// not possible, ReadFrom falls back to a generic copy.
func (p *Pipe) ReadFrom(src io.Reader) (int64, error) {
	return p.readFrom(src)
}

// WriteTo transfers data from the pipe to dst.
//
// If dst implements syscall.Conn, WriteTo tries to use splice(2) for the
// data transfer from the pipe to the destination file descriptor. If that
// is not possible, WriteTo falls back to a generic copy.
func (p *Pipe) WriteTo(dst io.Writer) (int64, error) {
	return p.writeTo(dst)
}

// Tee arranges for data in the read side of the pipe to be mirrored to the
// specified writer. There is no internal buffering: writes must complete
// before the associated read completes.
//
// If the argument is of concrete type *Pipe, the tee(2) system call
// is used when mirroring data from the read side of the pipe.
//
// Tee must not be called concurrently with I/O methods, and must be called
// only once, and before any calls to Read or WriteTo.
func (p *Pipe) Tee(w io.Writer) {
	p.tee(w)
}

// Transfer is like io.Copy, but moves data through a pipe rather than through
// a userspace buffer. Given a pipe p, Transfer operates equivalently to
// p.ReadFrom(src) and p.WriteTo(dst), but in lock-step, and with no need
// to create additional goroutines.
//
// Conceptually:
//
// 	Transfer(upstream, downstream)
//
// is equivalent to
//
// 	p, _ := NewPipe()
// 	go p.ReadFrom(downstream)
// 	p.WriteTo(upstream)
//
// but in more compact form, and slightly more resource-efficient.
func Transfer(dst io.Writer, src io.Reader) (int64, error) {
	return transfer(dst, src)
}
