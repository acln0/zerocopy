// Copyright (c) 2019 Andrei Tudor Călin <mail@acln.ro>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !linux

package zerocopy

import (
	"errors"
	"io"
)

func (p *Pipe) bufferSize() (int, error) {
	return 0, errors.New("not supported")
}

func (p *Pipe) setBufferSize(n int) error {
	return errors.New("not supported")
}

func (p *Pipe) read(b []byte) (n int, err error) {
	return p.teerd.Read(b)
}

func (p *Pipe) readFrom(src io.Reader) (int64, error) {
	return io.Copy(p.w, src)
}

func (p *Pipe) writeTo(dst io.Writer) (int64, error) {
	return io.Copy(dst, p.r)
}

func transfer(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}

func (p *Pipe) tee(w io.Writer) {
	p.teerd = io.TeeReader(p.r, w)
}
