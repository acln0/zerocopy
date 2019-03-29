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
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func (p *Pipe) read(b []byte) (int, error) {
	prefix := fmt.Sprintf("g=%d: ", getGID())
	l := log.New(os.Stdout, prefix, 0)
	l.Printf("ENTER: %d\n", len(b))
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
	readready := false
	waitread := false
	ok := false
	p.rrc.Read(func(prfd uintptr) bool {
		if readready {
			l.Printf("read ready\n")
		}
		wrcerr = p.teepipe.wrc.Write(func(pwfd uintptr) bool {
			copied, werr = tee(prfd, pwfd, len(b))
			l.Printf("tee(%d): %d, %v\n", len(b), copied, werr)
			if werr == unix.EAGAIN {
				if !readready {
					waitread = true
					return true
				}
				return false
			}
			if werr == nil {
				ok = true
			}
			werr = os.NewSyscallError("tee", werr)
			return true
		})
		if waitread {
			// The next time we enter this function, we will
			// be ready to read.
			readready = true
			waitread = false
			l.Println("waiting in read")
			return false
		}
		return true
	})
	_ = ok
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
	l.Println("entering p.teerd.Read")
	n, err := p.teerd.Read(b[:limit])
	if wrcerr != nil {
		return n, wrcerr
	}
	return n, err
}

const maxSpliceSize = 4 << 20

func (p *Pipe) readFrom(src io.Reader) (int64, error) {
	var (
		rd    io.Reader
		limit int64 = 1<<63 - 1
	)
	lr, ok := src.(*io.LimitedReader)
	if ok {
		rd = lr.R
		limit = lr.N
	} else {
		rd = src
	}
	sc, ok := rd.(syscall.Conn)
	if !ok {
		return io.Copy(p.w, src)
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0, err
	}

	var (
		atEOF  bool
		moved  int64
		werr   error
		wrcerr error
	)
again:
	firstwrite := true
	writeready := false
	max := maxSpliceSize
	if int64(max) > limit {
		max = int(limit)
	}
	err = rc.Read(func(rfd uintptr) bool {
		wrcerr = p.wrc.Write(func(pwfd uintptr) bool {
			var n int64
			n, werr = splice(rfd, pwfd, max)
			// TODO(acln): fall back on EINVAL
			limit -= n
			moved += n
			if werr == unix.EAGAIN {
				if firstwrite {
					firstwrite = false
					return false
				} else {
					writeready = true
					return true
				}
			}
			werr = os.NewSyscallError("splice", werr)
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
	if err != nil {
		return moved, err
	}
	if wrcerr != nil {
		return moved, wrcerr
	}
	if werr != nil {
		return moved, werr
	}
	if atEOF {
		return moved, nil
	}
	if limit > 0 {
		goto again
	}
	return moved, nil
}

func (p *Pipe) writeTo(dst io.Writer) (int64, error) {
	sc, ok := dst.(syscall.Conn)
	if !ok {
		return io.Copy(dst, onlyReader{p})
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0, err
	}

	var (
		atEOF  bool
		moved  int64
		werr   error
		wrcerr error
	)
again:
	firstwrite := true
	writeready := false
	err = p.rrc.Read(func(prfd uintptr) bool {
		wrcerr = rc.Write(func(wfd uintptr) bool {
			var n int64
			n, werr = splice(prfd, wfd, maxSpliceSize)
			// TODO(acln): fall back on EINVAL
			moved += n
			if werr == unix.EAGAIN {
				if firstwrite {
					firstwrite = false
					return false
				} else {
					writeready = true
					return true
				}
			}
			werr = os.NewSyscallError("splice", werr)
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
	if err != nil {
		return moved, err
	}
	if wrcerr != nil {
		return moved, wrcerr
	}
	if werr != nil {
		return moved, werr
	}
	if atEOF {
		return moved, nil
	}
	goto again
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

type onlyReader struct {
	io.Reader
}

// tee calls tee(2) with SPLICE_F_NONBLOCK.
func tee(rfd, wfd uintptr, max int) (int64, error) {
	return unix.Tee(int(rfd), int(wfd), max, unix.SPLICE_F_NONBLOCK)
}

// splice calls splice(2) with SPLICE_F_NONBLOCK.
func splice(rfd, wfd uintptr, max int) (int64, error) {
	return unix.Splice(int(rfd), nil, int(wfd), nil, max, unix.SPLICE_F_NONBLOCK)
}

func getGID() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	n, _ := strconv.ParseUint(string(b), 10, 64)
	return n
}
