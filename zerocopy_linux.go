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
	// There are three cases here:
	//
	// If p is not configured to tee data to another writer, then
	// p.teepipe is nil, and p.teerd is p.r.
	//
	// If p is configured to tee data to an io.Writer that is not a *Pipe,
	// then p.teepipe is nil, and p.teerd is an io.TeeReader of p.r and
	// the io.Writer.
	//
	// Finally, if p is configured to tee data to another *Pipe, then
	// p.teepipe is not nil, and p.teerd is p.r.
	if p.teepipe == nil {
		return p.teerd.Read(b)
	}

	// Here, we are on the tee(2) code path. When more than one stream of
	// data is involved, there are usually flow control considerations to
	// keep in mind, but here, we try to do the simple thing. We let the
	// size of the first tee guide us.
	//
	// Hopefully this approach is good enough for general use. Doing
	// anything else would be exceptionally complicated, and would require
	// the library to be either very configurable, or very opinionated.
	var (
		copied int64
		werr   error
		wrcerr error
	)
	firstwrite := true
	writeready := false
	ok := false
	p.rrc.Read(func(prfd uintptr) bool {
		wrcerr = p.teepipe.wrc.Write(func(pwfd uintptr) bool {
			copied, werr = tee(prfd, pwfd, len(b))
			if werr == unix.EAGAIN {
				if firstwrite {
					firstwrite = false
					return false
				} else {
					writeready = true
					return true
				}
			}
			werr = os.NewSyscallError("tee", werr)
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
	// We are deliberately ignoring the error from this last call to
	// p.rrc.Read: a Read on a syscall.RawConn only returns an error
	// if the file descriptor is closed. If the read side of the pipe
	// we own is indeed closed, the next call to Read on p.teerd will
	// observe this condition. In that case, we let the better error
	// reporting of package os kick in.
	//
	// As for write errors on the pipe we tee to, if the target FD is
	// closed, then the pipeline is dead anyway. All we've done so far
	// is to try to tee from p.r. We haven't consumed anything from the
	// pipe. Nevertheless, we should read from the pipe, but report
	// the dead pipe file descriptor as an error, since this is what
	// io.TeeReader does as well.
	//
	// Finally, we must be careful not to read more than we copied to
	// the other pipe, otherwise we will have missed data.
	limit := len(b)
	if copied > 0 {
		limit = int(copied)
	}
	n, err := p.teerd.Read(b[:limit])
	if wrcerr != nil {
		return n, wrcerr
	}
	return n, err
}

// tee calls tee(2) with SPLICE_F_NONBLOCK.
func tee(rfd, wfd uintptr, max int) (int64, error) {
	return unix.Tee(int(rfd), int(wfd), max, unix.SPLICE_F_NONBLOCK)
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

func (p *Pipe) tee(w io.Writer) {
	tp, ok := w.(*Pipe)
	if ok {
		p.teepipe = tp
	} else {
		p.teerd = io.TeeReader(p.r, w)
	}
}
