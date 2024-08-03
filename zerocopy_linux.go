// Copyright (c) 2018 The Go Authors https://golang.org/AUTHORS
// Copyright (c) 2019 Andrei Tudor Călin <mail@acln.ro>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zerocopy

/*
On Linux, package zerocopy uses splice(2) and tee(2) to accelerate I/O
pipelines. While most I/O system calls operate on a single file descriptor,
splice(2) and tee(2) operate on two. This makes using them from Go quite
tricky. Special precautions have to be taken in order to avoid a certain
class of deadlocks, and play nice with the outside world.

The algorithm to acheive this is rather complicated and subtle, but it is
used in a number of places in this package, with minor adaptations based on
context. This comment describes the algorithm. Because the real Go code that
implements the algorithm is much more messy, and involves more book-keeping
in terms of error handling, the algorithm is presented in pseudo-code.

Preparatory definitions
-----------------------

increfscope takes a file descriptor argument, acquires a reference to the
descriptor, runs the block of code, then releases the reference when control
exits the block (either naturally or via a goto statement).

transfer is a data transfer function, either splice(2) or tee(2).

wait waits for its file descriptor argument to be ready for an operation,
either 'r' for reading, or 'w' for writing.

The algorithm
-------------

	transfered = 0
	errno = nil

	round1:
	increfscope(rfd) {
		readready = false

		round1again:
		increfscope(wfd) {
			transfered, errno = transfer(rfd, wfd)
			if errno is EAGAIN {
				if readready {
					goto round2
				}
			} else {
				goto end
			}
		}

		wait(rfd, 'r')
		readready = true
		goto round1again
	}

	round2:
	increfscope(wfd) {
		writeready = false

		round2again:
		increfscope(rfd) {
			transfered, errno = transfer(rfd, wfd)
			if errno is EAGAIN {
				if writeready {
					goto round1
				}
			} else {
				goto end
			}
		}

		wait(wfd, 'w')
		writeready = true
		goto round2again
	}

	end:
	return transfered, errno

Restrictions, Analysis
----------------------

transfer can only be called while holding a reference to both file
descriptors. wait can only be called while holding a reference to the file
descriptor. It is forbidden to call wait on a file descriptor while also
holding a reference to another file descriptor.

Consider the naïve try:

increfscope(rfd) {
	increfscope(wfd) {
		again:
		transfered, errno = transfer(rfd, wfd)
		if errno is EAGAIN {
			wait(rfd, 'r')
			wait(wfd, 'w')
			goto again
		}
	}
}

This snippet is broken and dangerous.

If we're blocked in wait(wfd, 'w'), and in another place, a caller tries to
perform any operation on rfd, such as a hypothetical close(rfd), then the
other caller is blocked, waiting for an unrelated operation, on another
file descriptor.

The algorithm is so complicated because has to take great care to not end up
in a situation equivalent to this snippet. See also golang.org/issues/25985
for an example of such a bug, from the stdlib splice implementation.

Any changes to this package must retain these properties.
*/

import (
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func (p *Pipe) bufferSize() (int, error) {
	var (
		size  uintptr
		errno syscall.Errno
	)
	err := p.wrc.Control(func(fd uintptr) {
		size, _, errno = unix.Syscall(
			unix.SYS_FCNTL,
			fd,
			unix.F_GETPIPE_SZ,
			0,
		)
	})
	if err != nil {
		return 0, err
	}
	if errno != 0 {
		return 0, os.NewSyscallError("getpipesz", errno)
	}
	return int(size), nil
}

func (p *Pipe) setBufferSize(n int) error {
	var errno syscall.Errno
	err := p.wrc.Control(func(fd uintptr) {
		_, _, errno = unix.Syscall(
			unix.SYS_FCNTL,
			fd,
			unix.F_SETPIPE_SZ,
			uintptr(n),
		)
	})
	if err != nil {
		return err
	}
	if errno != 0 {
		return os.NewSyscallError("setpipesz", errno)
	}
	return nil
}

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
	//
	// HC SVNT DRACONES. See the comment at the top of the file.
	var (
		copied int64
		operr  error // error from tee(2)
		rrcerr error // non-nil if read FD is dead
		wrcerr error // non-nil if write FD is dead

		waitread      = false
		readready     = false
		done          = false
		writeready    = false
		waitwrite     = false
		waitreadagain = false
	)
again:
	rrcerr = p.rrc.Read(func(prfd uintptr) bool {
		wrcerr = p.teepipe.wrc.Write(func(pwfd uintptr) bool {
			copied, operr = tee(prfd, pwfd, len(b))
			if operr == unix.EAGAIN {
				if !readready {
					waitread = true
				}
				return true
			}
			done = true
			operr = os.NewSyscallError("tee", operr)
			return true
		})
		if waitread {
			readready = true
			waitread = false
			return false
		}
		return true
	})
	if rrcerr != nil || done {
		goto end
	}
	wrcerr = p.teepipe.wrc.Write(func(pwfd uintptr) bool {
		p.rrc.Read(func(prfd uintptr) bool {
			copied, operr = tee(prfd, pwfd, len(b))
			if operr == unix.EAGAIN {
				if writeready {
					waitreadagain = true
				} else {
					waitwrite = true
				}
				return true
			}
			operr = os.NewSyscallError("tee", operr)
			return true
		})
		if waitwrite {
			writeready = true
			waitwrite = false
			return false
		}
		return true
	})
	if wrcerr != nil {
		goto end
	}
	if waitreadagain {
		goto again
	}
end:
	// If rrcerr is not nil, we do not report it immediately: a Read on
	// a syscall.RawConn only returns an error if the file descriptor
	// is closed. If the read side of the pipe we own is indeed closed,
	// the next call to Read on p.teerd will observe this condition. In
	// that case, we let the better error reporting of package os kick in.
	//
	// As for write errors on the pipe we tee to, if the target FD is
	// closed, then the pipeline is dead anyway. All we've done so far
	// is to try to tee from p.r. We haven't consumed anything from the
	// pipe. Nevertheless, we should read from the pipe, but report
	// the dead pipe file descriptor as an error, since this is what
	// io.TeeReader does as well.
	//
	// Finally, we must be careful not to read more than we copied to
	// the other pipe, otherwise we will have missed tee-ing some data.
	limit := len(b)
	if copied > 0 {
		limit = int(copied)
	}
	n, err := p.teerd.Read(b[:limit])
	if wrcerr != nil {
		return n, wrcerr
	}
	if operr != nil {
		return n, operr
	}
	return n, err
}

const maxSpliceSize = 4 << 20

func (p *Pipe) readFrom(src io.Reader) (int64, error) {
	// If src is a limited reader, honor the limit.
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
	rrc, err := sc.SyscallConn()
	if err != nil {
		return io.Copy(p.w, src)
	}

	var (
		atEOF  bool
		moved  int64
		operr  error
		rrcerr error
		wrcerr error

		fallback      = false
		waitread      = false
		readready     = false
		writeready    = false
		waitwrite     = false
		waitreadagain = false
	)
	if lr != nil {
		defer func(v *int64) {
			lr.N -= *v
		}(&moved)
	}
again:
	ok = false
	max := maxSpliceSize
	if int64(max) > limit {
		max = int(limit)
	}
	rrcerr = rrc.Read(func(rfd uintptr) bool {
		wrcerr = p.wrc.Write(func(pwfd uintptr) bool {
			var n int
			n, operr = splice(rfd, pwfd, max)
			if n > 0 {
				limit -= int64(n)
				moved += int64(n)
			}
			if operr == unix.EINVAL {
				fallback = true
				return true
			}
			if operr == unix.EAGAIN {
				waitread = !readready
				return true
			}
			if operr == nil {
				if n == 0 {
					atEOF = true
				} else {
					ok = true
				}
			}
			operr = os.NewSyscallError("splice", operr)
			return true
		})
		if waitread {
			readready = true
			waitread = false
			return false
		}
		return true
	})
	if fallback {
		return io.Copy(p.w, src)
	}
	if wrcerr != nil || atEOF {
		return moved, wrcerr
	}
	if ok {
		if limit > 0 {
			goto again
		}
		goto end
	}
	// If we're here, we have not spliced yet on this round, and we're
	// waiting for the pipe to be ready.
	wrcerr = p.wrc.Write(func(pwfd uintptr) bool {
		rrcerr = rrc.Read(func(rfd uintptr) bool {
			var n int
			n, operr = splice(rfd, pwfd, max)
			if n > 0 {
				limit -= int64(n)
				moved += int64(n)
			}
			if operr == unix.EAGAIN {
				if writeready {
					waitwrite = false
					waitreadagain = true
				} else {
					waitwrite = true
				}
				return true
			}
			operr = os.NewSyscallError("splice", operr)
			return true
		})
		if waitwrite {
			writeready = true
			waitwrite = false
			return false
		}
		return true
	})
	if rrcerr != nil {
		return moved, rrcerr
	}
	if wrcerr != nil {
		return moved, wrcerr
	}
	if operr != nil {
		return moved, operr
	}
	if limit > 0 || waitreadagain {
		goto again
	}
end:
	return moved, nil
}

func (p *Pipe) writeTo(dst io.Writer) (int64, error) {
	sc, ok := dst.(syscall.Conn)
	if !ok {
		return io.Copy(dst, onlyReader{p})
	}
	wrc, err := sc.SyscallConn()
	if err != nil {
		return io.Copy(dst, onlyReader{p})
	}

	var (
		atEOF  bool
		moved  int64
		operr  error
		rrcerr error
		wrcerr error

		fallback      = false
		waitread      = false
		readready     = false
		writeready    = false
		waitwrite     = false
		waitreadagain = false
	)
again:
	ok = false
	rrcerr = p.rrc.Read(func(rfd uintptr) bool {
		wrcerr = wrc.Write(func(pwfd uintptr) bool {
			var n int
			n, operr = splice(rfd, pwfd, maxSpliceSize)
			if n > 0 {
				moved += int64(n)
			}
			if operr == unix.EINVAL {
				fallback = true
				return true
			}
			if operr == unix.EAGAIN {
				if !readready {
					waitread = true
				}
				return true
			}
			if operr == nil {
				if n == 0 {
					atEOF = true
				} else {
					ok = true
				}
			}
			operr = os.NewSyscallError("splice", operr)
			return true
		})
		if waitread {
			readready = true
			waitread = false
			return false
		}
		return true
	})
	if fallback {
		return io.Copy(dst, onlyReader{p})
	}
	if wrcerr != nil || atEOF {
		return moved, wrcerr
	}
	if ok {
		goto end
	}

	// If we're here, we have not spliced yet on this round, and we're
	// waiting for the destination file descriptor to be ready.
	wrcerr = wrc.Write(func(pwfd uintptr) bool {
		rrcerr = p.rrc.Read(func(rfd uintptr) bool {
			var n int
			n, operr = splice(rfd, pwfd, maxSpliceSize)
			if n > 0 {
				moved += int64(n)
			}
			if operr == unix.EAGAIN {
				if writeready {
					waitreadagain = true
				} else {
					waitwrite = true
				}
				return true
			}
			operr = os.NewSyscallError("splice", operr)
			return true
		})
		if waitwrite {
			writeready = true
			waitwrite = false
			return false
		}
		return true
	})
	if rrcerr != nil {
		return moved, rrcerr
	}
	if wrcerr != nil {
		return moved, wrcerr
	}
	if operr != nil {
		return moved, operr
	}
	if waitreadagain {
		goto again
	}
end:
	return moved, nil
}

func transfer(dst io.Writer, src io.Reader) (int64, error) {
	// If src is a limited reader, honor the limit.
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
	rsc, ok := rd.(syscall.Conn)
	if !ok {
		return io.Copy(dst, src)
	}
	rrc, err := rsc.SyscallConn()
	if err != nil {
		return io.Copy(dst, src)
	}

	wsc, ok := dst.(syscall.Conn)
	if !ok {
		return io.Copy(dst, src)
	}
	wrc, err := wsc.SyscallConn()
	if err != nil {
		return io.Copy(dst, src)
	}

	// Now, we know that dst and src are two file descriptors
	// that we could try to splice to / from, but we won't know
	// for sure until we actually try.
	//
	// See also src/internal/poll/splice_linux.go, which this code
	// is a pretty direct translation of.
	p, err := NewPipe()
	if err != nil {
		return io.Copy(dst, src)
	}
	defer p.Close()

	var moved int64 = 0
	if lr != nil {
		defer func(v *int64) {
			lr.N -= *v
		}(&moved)
	}
	for limit > 0 {
		max := maxSpliceSize
		if int64(max) > limit {
			max = int(limit)
		}
		inpipe, fallback, err := spliceDrain(p, rrc, max)
		limit -= int64(inpipe)
		if fallback {
			return io.Copy(dst, src)
		}
		if inpipe == 0 && err == nil {
			return moved, nil
		}
		if err != nil {
			return moved, err
		}
		n, fallback, err := splicePump(wrc, p, inpipe)
		if n > 0 {
			moved += int64(n)
		}
		if fallback {
			// dst doesn't support splicing, but we've already
			// read from src, so we need to empty the pipe,
			// and then switch to a regular io.Copy.
			n1, err := io.CopyN(dst, p.w, int64(inpipe))
			moved += n1
			if err != nil {
				return n1, err
			}
			n2, err := io.Copy(dst, src)
			return n1 + n2, err
		}
		if err != nil {
			return moved, err
		}
	}
	return moved, nil
}

func spliceDrain(p *Pipe, rrc syscall.RawConn, max int) (int, bool, error) {
	var (
		moved  int
		rrcerr error
		serr   error
	)
	fallback := false
	err := p.wrc.Write(func(pwfd uintptr) bool {
		rrcerr = rrc.Read(func(rfd uintptr) bool {
			var n int
			n, serr = splice(rfd, pwfd, max)
			moved = int(n)
			if serr == unix.EINVAL {
				fallback = true
				return true
			}
			if serr == unix.EAGAIN {
				return false
			}
			serr = os.NewSyscallError("splice", serr)
			return true
		})
		return true
	})
	if err != nil {
		return 0, false, err
	}
	if rrcerr != nil {
		return 0, false, rrcerr
	}
	return moved, fallback, serr
}

func splicePump(wrc syscall.RawConn, p *Pipe, inpipe int) (int, bool, error) {
	var (
		fallback bool
		moved    int
		wrcerr   error
		serr     error
	)
again:
	err := p.rrc.Read(func(prfd uintptr) bool {
		wrcerr = wrc.Write(func(wfd uintptr) bool {
			var n int
			n, serr = splice(prfd, wfd, inpipe)
			if n > 0 {
				moved += int(n)
				inpipe -= int(n)
			}
			if serr == unix.EINVAL {
				fallback = true
				return true
			}
			if serr == unix.EAGAIN {
				return false
			}
			serr = os.NewSyscallError("splice", serr)
			return true
		})
		return true
	})
	if fallback {
		return moved, true, nil
	}
	if err != nil {
		return moved, false, err
	}
	if wrcerr != nil {
		return moved, false, wrcerr
	}
	if serr != nil {
		return moved, false, serr
	}
	if inpipe > 0 {
		goto again
	}
	return moved, false, nil
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
func splice(rfd, wfd uintptr, max int) (int, error) {
	n, err := unix.Splice(int(rfd), nil, int(wfd), nil, max, unix.SPLICE_F_NONBLOCK)
	return int(n), err
}
