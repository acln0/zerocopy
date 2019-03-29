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
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func (p *Pipe) read(b []byte) (int, error) {
	if len(p.teepipes) == 0 {
		return p.teerd.Read(b)
	}

	// The first thing we must do is a tee from our read side of the
	// pipe to the first pipe registered in the tee. The caller asked
	// for at most len(b) bytes, so that is the upper bound for how much
	// we can move.
	//
	// Normally, there are flow control considerations to keep in mind for
	// such code, but since our pipeline runs in lock-step by definition,
	// we do the simple thing and let the size of the first transfer guide
	// us. In other words, we only try to move as much as we manage on
	// the first successful tee(2). This quantity is given by inPipe.
	//
	// Hopefully this approach is good enough for general use. Doing
	// anything else would be exceptionally complicated, and would require
	// the library to be either very configurable, or very opinionated.
	firstpipe := p.teepipes[0]
	var (
		moved  int64
		werr   error
		wrcerr error
	)
	firstwrite := true
	writeready := false
	ok := false
	p.rrc.Read(func(prfd uintptr) bool {
		wrcerr = firstpipe.wrc.Write(func(pwfd uintptr) bool {
			moved, werr = tee(prfd, pwfd, len(b))
			if werr == unix.EAGAIN {
				if firstwrite {
					firstwrite = false
					return false
				} else {
					writeready = true
					return true
				}
			}
			if werr == nil {
				ok = true
			}
			return true
		})
		if werr != nil {
			return true
		}
		if writeready && !ok {
			return false
		}
		return true
	})

	// This last call to p.rrc.Read either ends in EOF, failure or moves
	// some amount of data from p.r to firstpipe. If we have not moved
	// anything, don't try the other pipes.
	if moved == 0 {
		goto end
	}

	// Tee the known contents of the master pipe into all the remaining
	// ones. Unlike the first tee, here, we loop around if the pipes don't
	// accept all the data in one go, since our pipeline is defined to
	// operate in lock-step.
	p.rrc.Read(func(prfd uintptr) bool {
		for _, tp := range p.teepipes[1:] {
			if wrcerr != nil {
				break
			}
			remain := moved
			wrcerr = tp.wrc.Write(func(pwfd uintptr) bool {
				for remain > 0 {
					var written int64
					written, werr = tee(prfd, pwfd, int(remain))
					if werr == unix.EAGAIN {
						return false
					}
					remain -= written
					if werr != nil {
						return true
					}
				}
				return true
			})
		}
		return true
	})

end:
	// We are deliberately ignoring the error from calls to p.rrc.Read:
	//
	// A Read on a syscall.RawConn only returns an error if the file
	// descriptor is closed. If the read side of the pipe we own is
	// indeed closed, the next call to Read on p.teerd will observe
	// this condition. In that case, we let the better error reporting
	// of package os kick in.
	//
	// As for write errors on the pipes we tee to, if one of the pipe
	// file descriptors we must tee to is closed, then the pipeline is
	// dead anyway. All we've done so far is to try to tee from p.r. We
	// haven't consumed anything from the pipe. Nevertheless, we should
	// read from the pipe, but report the dead pipe file descriptor as
	// an error, since this is what io.TeeReader does as well.
	n, err := p.teerd.Read(b)
	if wrcerr != nil {
		return n, wrcerr
	}
	return n, err
}

// tee calls tee(2) with SPLICE_F_NONBLOCK, and wraps the error (if any)
// in an *os.SyscallError.
func tee(rfd, wfd uintptr, max int) (int64, error) {
	n, err := unix.Tee(int(rfd), int(wfd), max, unix.SPLICE_F_NONBLOCK)
	return n, os.NewSyscallError("tee", err)
}

func (p *Pipe) readFrom(src io.Reader) (int64, error) {
	return 0, errNotImplemented
}

func (p *Pipe) writeTo(dst io.Writer) (int64, error) {
	return 0, errNotImplemented
}

func (p *Pipe) transfer(dst io.Writer, src io.Reader) (int64, error) {
	return 0, errNotImplemented
}

func (p *Pipe) tee(ws ...io.Writer) {
	var (
		nonpipes []io.Writer
		teepipes []*Pipe
	)
	for _, w := range ws {
		tp, ok := w.(*Pipe)
		if ok {
			teepipes = append(teepipes, tp)
		} else {
			nonpipes = append(nonpipes, w)
		}
	}
	var teerd io.Reader
	switch len(nonpipes) {
	case 0:
		teerd = p.r
	case 1:
		teerd = io.TeeReader(p.r, nonpipes[0])
	default:
		teerd = io.TeeReader(p.r, io.MultiWriter(nonpipes...))
	}
	p.teerd = teerd
	p.teepipes = teepipes
}
